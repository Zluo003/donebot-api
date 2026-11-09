package service

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/jsonschema-go/jsonschema"
)

const DoneBotTemplateMaxBytes = 4 * 1024 * 1024

// Derived from OpenMausBot's Apache-2.0 package schema. Semantic checks remain required by the client import gate.
//
//go:embed templateschema/package-v2.schema.json
var doneBotPackageSchema []byte
var doneBotTemplateSchema = sync.OnceValues(func() (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(doneBotPackageSchema, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
})

type DoneBotTemplate struct {
	ID        string    `json:"id"`
	Release   string    `json:"release"`
	Name      string    `json:"name"`
	Summary   string    `json:"summary"`
	Category  string    `json:"category"`
	Kind      string    `json:"kind"`
	Members   []string  `json:"members"`
	Skills    []string  `json:"skills"`
	SHA256    string    `json:"sha256"`
	Bytes     int       `json:"bytes"`
	Published bool      `json:"published"`
	CreatedAt time.Time `json:"created_at"`
}
type DoneBotTemplateRelease struct {
	DoneBotTemplate
	Document json.RawMessage `json:"document"`
}
type DoneBotTemplateRepository interface {
	List(context.Context, bool) ([]DoneBotTemplate, error)
	Get(context.Context, string, string, bool) (*DoneBotTemplateRelease, error)
	Create(context.Context, *DoneBotTemplateRelease) error
	SetPublished(context.Context, string, string, bool) error
}
type DoneBotTemplateService struct{ repo DoneBotTemplateRepository }

func NewDoneBotTemplateService(repo DoneBotTemplateRepository) *DoneBotTemplateService {
	return &DoneBotTemplateService{repo: repo}
}

var ErrDoneBotTemplateMissing = infraerrors.NotFound("DONEBOT_TEMPLATE_NOT_FOUND", "模板版本不存在或已下架")
var ErrDoneBotTemplateExists = infraerrors.Conflict("DONEBOT_TEMPLATE_EXISTS", "此版本已存在，请修改版本号后发布")

func (s *DoneBotTemplateService) List(ctx context.Context, admin bool) ([]DoneBotTemplate, error) {
	return s.repo.List(ctx, admin)
}
func (s *DoneBotTemplateService) Get(ctx context.Context, id, release string, admin bool) (*DoneBotTemplateRelease, error) {
	return s.repo.Get(ctx, id, release, admin)
}
func (s *DoneBotTemplateService) SetPublished(ctx context.Context, id, release string, published bool) error {
	return s.repo.SetPublished(ctx, id, release, published)
}
func (s *DoneBotTemplateService) Publish(ctx context.Context, raw json.RawMessage) (*DoneBotTemplateRelease, error) {
	entry, err := PreviewDoneBotTemplate(raw)
	if err != nil {
		return nil, err
	}
	if err = s.repo.Create(ctx, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

//nolint:errcheck // JSON Schema validation guarantees the field types used by the assertions below.
func PreviewDoneBotTemplate(raw json.RawMessage) (*DoneBotTemplateRelease, error) {
	invalid := func(message string) (*DoneBotTemplateRelease, error) {
		return nil, infraerrors.BadRequest("DONEBOT_TEMPLATE_INVALID", message)
	}
	if len(raw) > DoneBotTemplateMaxBytes {
		return invalid("模板包不能超过 4 MB")
	}
	if err := validateDoneBotNativeJSON(raw, false); err != nil {
		return invalid("模板必须是有效且没有重复字段的 JSON")
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return invalid("模板必须是 JSON 对象")
	}
	schema, err := doneBotTemplateSchema()
	if err != nil {
		return nil, err
	}
	if err = schema.Validate(root); err != nil {
		return invalid(fmt.Sprintf("模板格式错误：%s", err))
	}
	pkg := root["package"].(map[string]any)
	delete(pkg, "publisher")
	agents, _ := pkg["agents"].([]any)
	presets, _ := pkg["presets"].([]any)
	kind := "bot"
	if len(agents) > 0 {
		kind = "department"
		if pkg["team"] == nil {
			return invalid("部门模板缺少部门设置")
		}
	} else if len(presets) != 1 {
		return invalid("机器人模板应只包含一个机器人；部门模板使用 agents 和 team")
	}
	if len(agents) == 0 && (pkg["team"] != nil || len(doneBotTemplateList(pkg, "rooms")) > 0 || len(doneBotTemplateList(pkg, "routines")) > 0) {
		return invalid("单体机器人模板不能包含部门、群聊或自动化")
	}
	keys := map[string]map[string]bool{}
	for _, field := range []string{"agents", "presets", "rooms", "playbooks", "routines", "connections"} {
		keys[field] = map[string]bool{}
		list, _ := pkg[field].([]any)
		for _, value := range list {
			item := value.(map[string]any)
			key, _ := item["key"].(string)
			if keys[field][key] {
				return invalid("重复的模板标识：" + key)
			}
			keys[field][key] = true
		}
	}
	skills := []string{}
	keys["skills"] = map[string]bool{}
	if bundle, ok := pkg["skills"].(map[string]any); ok {
		for _, value := range bundle["entries"].([]any) {
			item := value.(map[string]any)
			name := item["name"].(string)
			if !validDoneBotTemplateSkill(item) {
				return invalid("技能内容与声明不一致，请检查 SKILL.md：" + name)
			}
			if keys["skills"][name] {
				return invalid("重复的技能：" + name)
			}
			keys["skills"][name] = true
			skills = append(skills, name)
		}
	}
	hasRefs := func(item map[string]any, field, target string) bool {
		list, _ := item[field].([]any)
		seen := map[string]bool{}
		for _, v := range list {
			key, ok := v.(string)
			if !ok || !keys[target][key] || seen[key] {
				return false
			}
			seen[key] = true
		}
		return true
	}
	members := []string{}
	for _, value := range append(append([]any{}, agents...), presets...) {
		item := value.(map[string]any)
		if strings.TrimSpace(item["name"].(string)) == "" || strings.ContainsAny(item["name"].(string), "\r\n\u2028\u2029") {
			return invalid("机器人名称必须在一行内")
		}
		bot := item
		if nested, ok := item["bot"].(map[string]any); ok {
			bot = nested
		}
		for _, field := range []string{"name", "title"} {
			if value, ok := bot[field].(string); ok && strings.ContainsAny(value, "\r\n\u2028\u2029") {
				return invalid("机器人名称和岗位必须在一行内")
			}
		}
		soul, _ := bot["soul"].(string)
		if strings.TrimSpace(soul) == "" || len(soul) > 24000 {
			return invalid("每个机器人必须包含完整 SOUL，且不能超过 24000 字节")
		}
		if !hasRefs(item, "skills", "skills") || !hasRefs(item, "playbooks", "playbooks") || !hasRefs(item, "connections", "connections") {
			return invalid("机器人引用了不存在的技能、工作手册或连接")
		}
	}
	memberEntries := agents
	if len(memberEntries) == 0 {
		memberEntries = presets
	}
	for _, value := range memberEntries {
		members = append(members, value.(map[string]any)["name"].(string))
	}
	if team, ok := pkg["team"].(map[string]any); ok {
		if leader, ok := team["leader"].(string); ok && !keys["agents"][leader] {
			return invalid("部门负责人不在成员中")
		}
	}
	roomMembers := map[string]map[string]bool{}
	if rooms, ok := pkg["rooms"].([]any); ok {
		for _, value := range rooms {
			room := value.(map[string]any)
			if !hasRefs(room, "members", "agents") {
				return invalid("群聊引用了不存在的成员")
			}
			members := map[string]bool{}
			for _, member := range room["members"].([]any) {
				members[member.(string)] = true
			}
			roomMembers[room["key"].(string)] = members
			if responder, ok := room["defaultResponder"].(map[string]any); ok {
				if agent, ok := responder["agent"].(string); ok && !members[agent] {
					return invalid("群聊默认回复者不存在")
				}
			}
		}
	}
	if routines, ok := pkg["routines"].([]any); ok {
		for _, value := range routines {
			routine := value.(map[string]any)
			agent, _ := routine["agent"].(string)
			if !keys["agents"][agent] {
				return invalid("自动化引用了不存在的成员")
			}
			if room, ok := routine["room"].(string); ok {
				if !roomMembers[room][agent] || routine["runOn"] != "maus" || routine["continuity"] != nil {
					return invalid("群聊自动化的房间、执行方式或负责人设置无效")
				}
			}
		}
	}
	canonical, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	if len(canonical) > DoneBotTemplateMaxBytes {
		return invalid("模板包不能超过 4 MB")
	}
	digest := sha256.Sum256(canonical)
	return &DoneBotTemplateRelease{DoneBotTemplate: DoneBotTemplate{
		ID: pkg["id"].(string), Release: pkg["release"].(string), Name: pkg["name"].(string), Summary: pkg["summary"].(string), Category: pkg["category"].(string),
		Kind: kind, Members: members, Skills: skills, SHA256: hex.EncodeToString(digest[:]), Bytes: len(canonical), Published: true,
	}, Document: canonical}, nil
}

func doneBotTemplateList(item map[string]any, field string) []any {
	list, _ := item[field].([]any)
	return list
}

var doneBotTemplateFrontmatter = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n?(.*)$`)
var doneBotTemplateSkillField = regexp.MustCompile(`^([A-Za-z][\w-]*):\s*(.*)$`)
var doneBotTemplateSkillName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// This mirrors the Apache-2.0 OpenMausBot single-line SKILL.md scalar contract, not full YAML evaluation.
func validDoneBotTemplateSkill(item map[string]any) bool {
	instructions, _ := item["instructions"].(string)
	if !utf8.ValidString(instructions) || len(instructions) > 256*1024 {
		return false
	}
	match := doneBotTemplateFrontmatter.FindStringSubmatch(instructions)
	if match == nil {
		return false
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(match[1], "\r\n", "\n"), "\n") {
		pair := doneBotTemplateSkillField.FindStringSubmatch(line)
		if pair == nil {
			continue
		}
		value := strings.TrimSpace(pair[2])
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		} else if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			inner := value[1 : len(value)-1]
			var scalar strings.Builder
			for i := 0; i < len(inner); i++ {
				if inner[i] == '\\' && i+1 < len(inner) && (inner[i+1] == '"' || inner[i+1] == '\\' || inner[i+1] == 'n') {
					i++
					if inner[i] == 'n' {
						_ = scalar.WriteByte('\n')
					} else {
						_ = scalar.WriteByte(inner[i])
					}
				} else {
					_ = scalar.WriteByte(inner[i])
				}
			}
			value = scalar.String()
		}
		fields[strings.ToLower(pair[1])] = value
	}
	if len(fields["name"]) > 64 || !doneBotTemplateSkillName.MatchString(fields["name"]) || fields["description"] == "" {
		return false
	}
	for _, field := range []string{"name", "description", "license", "compatibility"} {
		if value, ok := item[field].(string); ok && value != fields[field] {
			return false
		}
	}
	return true
}

var doneBotTemplateID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)
var doneBotTemplateVersion = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

func ValidDoneBotTemplatePath(id, version string) bool {
	return doneBotTemplateID.MatchString(id) && doneBotTemplateVersion.MatchString(version)
}
