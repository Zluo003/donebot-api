package nickname

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxRunes = 64

var (
	ErrTooLong   = errors.New("nickname_too_long")
	ErrControl   = errors.New("nickname_invalid_chars")
	ErrForbidden = errors.New("nickname_forbidden")
)

// 高危英文/拼音子串：去除空格与符号后仍禁止出现在昵称任意位置（同时拦截常见数字/符号 leet 伪装）
var forbiddenASCIISubstrings = []string{
	// 系统与超级权限
	"root",
	"admin",
	"administrator",
	"sysadmin",
	"superadmin",
	"superuser",
	"webmaster",
	"postmaster",
	"hostmaster",
	"sysop",
	"moderator",
	"supervisor",
	"system",
	"daemon",
	"noreply",
	"donotreply",
	// 平台品牌与官方仿冒
	"donebot",
	"openmaus",
	"opengrokbot",
	"sub2api",
	"official",
	"guanfang",
	"botmarket",
	"botstore",
	"marketplace",
	// 客服、安全、审核、财务仿冒
	"support",
	"helpdesk",
	"customerservice",
	"custservice",
	"helpcenter",
	"security",
	"antifraud",
	"compliance",
	"verified",
	"billing",
	"payment",
	"finance",
	"kefu",
	"guanliyuan",
	"guanli",
	"xitong",
	"yunying",
	"yunwei",
	"shenhe",
	"fengkong",
}

// 中文高危词：去除空格、标点与符号后禁止出现在昵称任意位置
var forbiddenCJKSubstrings = []string{
	// 权限与管理
	"管理员",
	"超管",
	"超级管理",
	"总管",
	"系统管理",
	"后台管理",
	"站长",
	"版主",
	"网管",
	"管理者",
	// 官方、平台与内部身份仿冒
	"官方",
	"官微",
	"官号",
	"官博",
	"官服",
	"正版授权",
	"认证账号",
	"认证账户",
	"认证号",
	"蓝v",
	"内部人员",
	"内部员工",
	"内部号",
	"内部渠道",
	"平台方",
	"平台账号",
	"平台账户",
	"平台助手",
	"平台服务",
	"主办方",
	"运营方",
	"开发团队",
	"研发团队",
	"技术团队",
	"产品团队",
	"核心团队",
	"创始团队",
	"创始人",
	"联合创始人",
	"项目组",
	"工作组",
	"总部",
	"总裁办",
	"服务号",
	"公众号",
	// 系统、通知、审核与风控
	"系统",
	"通知中心",
	"安全中心",
	"风控",
	"审核",
	"审查",
	"质检",
	"合规",
	"运维",
	"运营",
	"封号",
	"解封",
	"申诉",
	"黑名单",
	"实名认证",
	"身份验证",
	"验证码",
	// 客服、售后、财务与诈骗引流
	"客服",
	"售后",
	"客诉",
	"人工服务",
	"服务台",
	"帮助中心",
	"技术支持",
	"服务支持",
	"用户支持",
	"店小二",
	"值班",
	"安全专员",
	"反诈",
	"反欺诈",
	"防骗",
	"财务",
	"结算",
	"充值",
	"支付",
	"账单",
	"提现",
	"收银台",
	"计费",
	"算力中心",
	"代充",
	"返利",
	"刷单",
	"中奖",
	"领奖",
	"派奖",
	"法务",
	"仲裁",
	// 公检法、监管与权威机构仿冒
	"公安",
	"警察",
	"网警",
	"民警",
	"交警",
	"法院",
	"检察院",
	"税务",
	"工商",
	"监管",
	"网信办",
	"工信部",
	"证监会",
	"银保监",
	"央行",
	"人民政府",
	"国务院",
	"外交部",
	"新华社",
	"人民日报",
	"央视",
	"权威发布",
}

// 短英文高危词：按独立词边界拦截，避免误杀正常单词子串
var forbiddenASCIITokens = map[string]struct{}{
	"su":     {},
	"sudo":   {},
	"wheel":  {},
	"mod":    {},
	"mods":   {},
	"gm":     {},
	"ops":    {},
	"devops": {},
}

// 整串精确拦截：仅当整个昵称（去空白和标点后）等于以下词时拒绝，避免误杀复合正常昵称（如 Product Owner）
var forbiddenExact = map[string]struct{}{
	"owner":     {},
	"master":    {},
	"bot":       {},
	"team":      {},
	"staff":     {},
	"service":   {},
	"verify":    {},
	"auth":      {},
	"server":    {},
	"localhost": {},
	"guest":     {},
	"anonymous": {},
	"null":      {},
	"undefined": {},
	"none":      {},
	"unknown":   {},
	"founder":   {},
	"平台":        {},
	"团队":        {},
	"助手":        {},
	"小助手":       {},
	"机器人":       {},
	"匿名":        {},
	"游客":        {},
	"未命名":       {},
	"未知":        {},
}

func foldFullwidth(r rune) rune {
	if r == '\u3000' {
		return ' '
	}
	if r >= 0xFF01 && r <= 0xFF5E {
		return r - 0xFEE0
	}
	return r
}

func isZeroWidthOrFormat(r rune) bool {
	if unicode.Is(unicode.Cf, r) {
		return true
	}
	switch r {
	case '\u00AD', '\u200B', '\u200C', '\u200D', '\u200E', '\u200F', '\u2060', '\uFEFF':
		return true
	}
	return false
}

func mapLeet(r rune) rune {
	switch r {
	case '0':
		return 'o'
	case '1', '!':
		return 'i'
	case '3':
		return 'e'
	case '4', '@':
		return 'a'
	case '5', '$':
		return 's'
	case '7':
		return 't'
	case '8':
		return 'b'
	default:
		return r
	}
}

// Validate checks a user-provided nickname (already trimmed or untrimmed).
// Empty nicknames are allowed (clears custom nickname).
func Validate(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if utf8.RuneCountInString(trimmed) > MaxRunes {
		return ErrTooLong
	}

	var (
		compact     strings.Builder
		leetCompact strings.Builder
		asciiTokens []string
		curToken    strings.Builder
	)

	flushToken := func() {
		if curToken.Len() > 0 {
			asciiTokens = append(asciiTokens, curToken.String())
			curToken.Reset()
		}
	}

	for _, r := range trimmed {
		if r < 0x20 || r == 0x7F {
			return ErrControl
		}
		if isZeroWidthOrFormat(r) {
			continue
		}
		folded := unicode.ToLower(foldFullwidth(r))

		if (folded >= 'a' && folded <= 'z') || (folded >= '0' && folded <= '9') {
			_, _ = curToken.WriteRune(folded)
		} else {
			flushToken()
		}

		if unicode.IsLetter(folded) || unicode.IsDigit(folded) {
			_, _ = compact.WriteRune(folded)
		}

		leet := mapLeet(folded)
		if unicode.IsLetter(leet) || unicode.IsDigit(leet) {
			_, _ = leetCompact.WriteRune(leet)
		}
	}
	flushToken()

	compactStr := compact.String()
	leetStr := leetCompact.String()
	if compactStr == "" {
		return ErrForbidden
	}

	if _, blocked := forbiddenExact[compactStr]; blocked {
		return ErrForbidden
	}
	for _, tok := range asciiTokens {
		if _, blocked := forbiddenASCIITokens[tok]; blocked {
			return ErrForbidden
		}
	}
	for _, word := range forbiddenASCIISubstrings {
		if strings.Contains(compactStr, word) || strings.Contains(leetStr, word) {
			return ErrForbidden
		}
	}
	for _, word := range forbiddenCJKSubstrings {
		if strings.Contains(compactStr, word) {
			return ErrForbidden
		}
	}
	return nil
}

// SanitizeOAuthDisplayName returns a trimmed nickname capped at MaxRunes,
// or empty string if the upstream OAuth display name violates nickname policy.
func SanitizeOAuthDisplayName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) > MaxRunes {
		trimmed = string(runes[:MaxRunes])
	}
	if Validate(trimmed) != nil {
		return ""
	}
	return trimmed
}
