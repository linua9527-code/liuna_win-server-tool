package main

import (
	"fmt"
	"strings"
)

/** @brief 表单字段统一处理默认值、可选字符串和数字范围。 */
type formField struct {
	Label    string
	Boolean  *bool
	Integer  *int
	Text     *string
	Optional bool
	Minimum  int
	Maximum  int
}

func (c *Console) form(fields ...formField) error {
	for _, field := range fields {
		switch {
		case field.Boolean != nil:
			value, err := c.boolean(field.Label, *field.Boolean)
			if err != nil {
				return err
			}
			*field.Boolean = value
		case field.Integer != nil:
			value, err := c.integer(field.Label, *field.Integer, field.Minimum, field.Maximum)
			if err != nil {
				return err
			}
			*field.Integer = value
		case field.Text != nil:
			var value string
			var err error
			if field.Optional {
				value, err = c.optionalText(field.Label, *field.Text)
			} else {
				value, err = c.text(field.Label, *field.Text)
			}
			if err != nil {
				return err
			}
			*field.Text = value
		}
	}
	return nil
}

func (a *Application) editOptions(session *interactiveSession) error {
	for {
		fmt.Fprintln(a.Console.Writer, "\n配置选项")
		a.showConfiguration(session.Config)
		fmt.Fprintln(a.Console.Writer, "1. 远程桌面与登录锁定\n2. 时区\n3. 语言\n4. 登录密码\n5. 清理范围\n6. 定时清理时间\n0. 返回")
		choice, err := a.Console.line("请选择：")
		if err != nil {
			return err
		}
		if choice == "0" {
			return nil
		}
		if choice < "1" || choice > "6" || len(choice) != 1 {
			fmt.Fprintln(a.Console.Writer, "请选择菜单中的编号。")
			continue
		}
		if err := a.editSection(choice, session); err != nil {
			return err
		}
	}
}

/** @brief 在草稿中编辑完整分组，输入中断或校验失败时保留原配置。 */
func (a *Application) editSection(choice string, session *interactiveSession) error {
	draft := session.Config
	var err error
	switch choice {
	case "1":
		err = a.editRDP(&draft.RDP)
	case "2":
		err = a.Console.form(formField{Label: "时区 ID（上海：China Standard Time）", Text: &draft.TimeZone, Optional: true})
	case "3":
		err = a.editLanguage(&draft.Language)
	case "4":
		err = a.Console.form(formField{Label: "修改登录密码", Boolean: &draft.Password.Change})
		if err == nil && draft.Password.Change {
			err = a.Console.form(formField{Label: "本地用户名（留空为当前用户）", Text: &draft.Password.Username, Optional: true})
		}
	case "5":
		err = a.editCleanup(&draft.Cleanup)
	case "6":
		err = a.Console.form(formField{Label: "每日执行时间（HH:mm）", Text: &draft.Schedule.DailyAt})
	default:
		return fmt.Errorf("未知配置分组：%s", choice)
	}
	if err != nil {
		return err
	}
	if err := draft.validate(); err != nil {
		return err
	}
	session.Config, session.Dirty = draft, true
	fmt.Fprintln(a.Console.Writer, "配置选项已更新。")
	return nil
}

func (a *Application) editRDP(c *RDPConfig) error {
	ui := a.Console
	if err := ui.form(formField{Label: "配置远程桌面", Boolean: &c.Enabled}); err != nil {
		return err
	}
	if c.Enabled {
		if err := ui.form(
			formField{Label: "远程桌面端口", Integer: &c.Port, Minimum: 1, Maximum: 65535},
			formField{Label: "启用 NLA", Boolean: &c.RequireNLA},
		); err != nil {
			return err
		}
		value, err := ui.text("防火墙允许来源（IP/CIDR、Any 或 LocalSubnet，逗号分隔）", strings.Join(c.RemoteAddresses, ","))
		if err != nil {
			return err
		}
		c.RemoteAddresses = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == ' ' || r == '\t' })
	}
	if err := ui.form(formField{Label: "配置登录失败锁定", Boolean: &c.Lockout}); err != nil {
		return err
	}
	if c.Lockout {
		if err := ui.form(
			formField{Label: "登录失败次数阈值", Integer: &c.Threshold, Minimum: 1, Maximum: 999},
			formField{Label: "账户锁定时长（分钟）", Integer: &c.DurationMinutes, Minimum: 1, Maximum: 99999},
		); err != nil {
			return err
		}
		return ui.form(formField{Label: "失败计数重置时间（分钟）", Integer: &c.ResetMinutes, Minimum: 1, Maximum: c.DurationMinutes})
	}
	return nil
}

func (a *Application) editLanguage(c *LanguageConfig) error {
	if err := a.Console.form(formField{Label: "调整语言", Boolean: &c.Enabled}); err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}
	if err := a.Console.form(
		formField{Label: "语言标识", Text: &c.Tag},
		formField{Label: "缺少语言包时安装", Boolean: &c.InstallIfMissing},
	); err != nil {
		return err
	}
	if c.InstallIfMissing {
		if err := a.Console.form(formField{Label: "语言包 CAB 绝对路径（留空使用系统在线安装）", Text: &c.PackageCAB, Optional: true}); err != nil {
			return err
		}
	}
	return a.Console.form(formField{Label: "同步到欢迎屏幕和新用户", Boolean: &c.CopyToSystem})
}

func (a *Application) editCleanup(c *CleanupConfig) error {
	if err := a.Console.form(
		formField{Label: "清理远程桌面历史", Boolean: &c.RDPHistory},
		formField{Label: "清理保存的远程凭据", Boolean: &c.RDPCredentials},
		formField{Label: "清理最近访问、运行记录与 PowerShell 输入历史", Boolean: &c.RecentFiles},
		formField{Label: "清理临时文件", Boolean: &c.TempFiles},
	); err != nil {
		return err
	}
	return a.Console.form(formField{Label: "清空 Windows 事件日志", Boolean: &c.EventLogs})
}
