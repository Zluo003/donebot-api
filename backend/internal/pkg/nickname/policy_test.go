package nickname

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate_AllowsLegitimateNicknames(t *testing.T) {
	allowed := []string{
		"",
		"   ",
		"老罗",
		"阿泽",
		"Product Lead",
		"Product Owner",
		"Alice Example",
		"Bob 昵称",
		"码匠",
		"独立站卖家",
		"Python工程师",
		"AI绘画师",
		"owner-user",
		"custom-name",
		"tester",
		"avatar-user",
		"modern-dev",
		"sara_lee",
		"RobotLover",
		"编程小助手2026",
	}
	for _, name := range allowed {
		require.NoError(t, Validate(name), "expected nickname %q to be allowed", name)
	}
}

func TestValidate_RejectsDangerousAndImpersonationNicknames(t *testing.T) {
	rejected := []string{
		"root",
		"ROOT",
		"r00t",
		"r.o.o.t",
		"super_root",
		"admin",
		"Admin",
		"ADM1N",
		"@dmin",
		"a-d-m-i-n",
		"Ａｄｍｉｎ",
		"administrator",
		"Administrator",
		"sysadmin",
		"管理员",
		"管 理 员",
		"管·理·员",
		"管\u200b理\u200b员",
		"超级管理员",
		"客服",
		"客_服",
		"在线客服",
		"人工客服",
		"DoneBot",
		"donebot",
		"Done-Bot",
		"D0neB0t",
		"DoneBot官方",
		"官方",
		"官 方",
		"官方认证",
		"官微",
		"official",
		"0ff1c1al",
		"OpenMausBot",
		"系统通知",
		"安全中心",
		"风控审核",
		"support",
		"helpdesk",
		"moderator",
		"noreply",
		"财务结算",
		"充值中心",
		"网警",
		"公安",
		"sudo",
		"mod",
		"ops",
		"owner",
		"bot",
		"平台",
		"团队",
		"小助手",
		"!!!",
		"bad\nname",
	}
	for _, name := range rejected {
		require.Error(t, Validate(name), "expected nickname %q to be rejected", name)
	}
}

func TestValidate_EnforcesMaxLength(t *testing.T) {
	require.NoError(t, Validate(strings.Repeat("罗", MaxRunes)))
	require.ErrorIs(t, Validate(strings.Repeat("罗", MaxRunes+1)), ErrTooLong)
}

func TestSanitizeOAuthDisplayName(t *testing.T) {
	require.Equal(t, "Alice Example", SanitizeOAuthDisplayName("  Alice Example  "))
	require.Equal(t, "", SanitizeOAuthDisplayName("DoneBot 官方"))
	require.Equal(t, "", SanitizeOAuthDisplayName("admin"))
}
