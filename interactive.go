package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type interactiveSession struct {
	Config Config
	Path   string
	Dirty  bool
}

/** @brief 无配置文件时使用内存默认值，查看与退出菜单不创建或修改文件。 */
func (a *Application) interactive(path string) error {
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	session := interactiveSession{Config: defaultConfig(), Path: fullPath}
	if _, err := os.Stat(fullPath); err == nil {
		session.Config, err = loadConfig(fullPath)
		if err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		session.Dirty = true
	} else {
		return err
	}
	for {
		fmt.Fprintln(a.Console.Writer, "\nWinServerTool")
		saved := ""
		if session.Dirty {
			saved = "（未保存）"
		}
		fmt.Fprintln(a.Console.Writer, "配置：", session.Path+saved)
		fmt.Fprintln(a.Console.Writer, "1. 一键配置并清理\n2. 系统配置\n3. 清理记录\n4. 配置选项\n5. 定时清理\n6. 系统状态\n7. 配置预检\n8. 读取配置\n9. 保存配置\n0. 退出")
		choice, err := a.Console.line("请选择：")
		if errors.Is(err, errConsoleClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		switch choice {
		case "0":
			return nil
		case "1", "2", "3":
			err = a.interactiveExecute(choice, &session)
		case "4":
			err = a.editOptions(&session)
		case "5":
			err = a.interactiveSchedule(&session)
		case "6":
			err = a.status()
		case "7":
			err = a.preflight(session.Config)
		case "8":
			err = a.readInteractiveConfig(&session)
		case "9":
			err = a.saveInteractiveConfig(&session)
		default:
			fmt.Fprintln(a.Console.Writer, "请选择菜单中的编号。")
		}
		if errors.Is(err, errConsoleClosed) {
			return nil
		}
		if err != nil {
			fmt.Fprintln(a.Console.Writer, "错误：", err)
		}
	}
}

func enabledText(value bool) string {
	if value {
		return "开启"
	}
	return "关闭"
}

func cleanupSummary(c CleanupConfig) string {
	var names []string
	for _, item := range []struct {
		name    string
		enabled bool
	}{
		{"远程桌面历史", c.RDPHistory}, {"远程凭据", c.RDPCredentials},
		{"最近访问与输入历史", c.RecentFiles}, {"临时文件", c.TempFiles}, {"事件日志", c.EventLogs},
	} {
		if item.enabled {
			names = append(names, item.name)
		}
	}
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, "、")
}

func (a *Application) showConfiguration(c Config) {
	a.showSystemConfiguration(c)
	a.showCleanupConfiguration(c)
	fmt.Fprintf(a.Console.Writer, "定时清理：每天 %s\n", c.Schedule.DailyAt)
}

func (a *Application) showSystemConfiguration(c Config) {
	w := a.Console.Writer
	fmt.Fprintf(w, "远程桌面配置：%s；端口：%d；NLA：%s\n", enabledText(c.RDP.Enabled), c.RDP.Port, enabledText(c.RDP.RequireNLA))
	fmt.Fprintf(w, "防火墙来源：%s\n", strings.Join(c.RDP.RemoteAddresses, ", "))
	fmt.Fprintf(w, "登录失败锁定：%s；阈值：%d；锁定/重置：%d/%d 分钟\n", enabledText(c.RDP.Lockout), c.RDP.Threshold, c.RDP.DurationMinutes, c.RDP.ResetMinutes)
	fmt.Fprintf(w, "时区：%s；语言设置：%s（%s）\n", c.TimeZone, enabledText(c.Language.Enabled), c.Language.Tag)
	username := c.Password.Username
	if username == "" {
		username = "当前本地用户"
	}
	fmt.Fprintf(w, "修改密码：%s（%s）\n", enabledText(c.Password.Change), username)
}

func (a *Application) showCleanupConfiguration(c Config) {
	fmt.Fprintf(a.Console.Writer, "清理：%s\n", cleanupSummary(c.Cleanup))
}

func (a *Application) interactiveExecute(choice string, session *interactiveSession) error {
	config := session.Config
	if err := config.validate(); err != nil {
		return err
	}
	if choice != "3" {
		a.showSystemConfiguration(config)
	}
	if choice != "2" {
		a.showCleanupConfiguration(config)
	}
	if choice == "1" || choice == "2" {
		if config.RDP.Enabled {
			fmt.Fprintf(a.Console.Writer, "请先在云平台安全组放行 TCP/UDP %d；新端口重启后生效。\n", config.RDP.Port)
		}
	}
	switch choice {
	case "1":
		return a.setup(config, session.Path)
	case "2":
		return a.apply(config, session.Path)
	case "3":
		return a.clean(Request{Action: "clean", Config: config, ConfigPath: session.Path})
	}
	return nil
}

func (a *Application) interactiveSchedule(session *interactiveSession) error {
	for {
		fmt.Fprintf(a.Console.Writer, "\n定时清理：每天 %s\n清理：%s\n1. 安装或更新任务\n2. 修改执行时间\n3. 删除任务\n0. 返回\n", session.Config.Schedule.DailyAt, cleanupSummary(session.Config.Cleanup))
		choice, err := a.Console.line("请选择：")
		if err != nil {
			return err
		}
		switch choice {
		case "0":
			return nil
		case "1":
			return a.installSchedule(session.Config)
		case "2":
			if err := a.editSection("6", session); err != nil {
				return err
			}
		case "3":
			confirmed, err := a.Console.boolean("删除本工具的清理任务", false)
			if err != nil {
				return err
			}
			if confirmed {
				return a.removeSchedule()
			}
		default:
			fmt.Fprintln(a.Console.Writer, "请选择菜单中的编号。")
		}
	}
}

func (a *Application) readInteractiveConfig(session *interactiveSession) error {
	path, err := a.Console.text("配置文件路径", session.Path)
	if err != nil {
		return err
	}
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	config, err := loadConfig(fullPath)
	if err != nil {
		return err
	}
	if session.Dirty {
		confirmed, err := a.Console.boolean("替换尚未保存的配置选项", false)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}
	*session = interactiveSession{Config: config, Path: fullPath}
	fmt.Fprintln(a.Console.Writer, "已读取配置。")
	return nil
}

func (a *Application) saveInteractiveConfig(session *interactiveSession) error {
	path, err := a.Console.text("保存路径", session.Path)
	if err != nil {
		return err
	}
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(fullPath); err == nil {
		confirmed, err := a.Console.boolean("覆盖已有配置文件", false)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := session.Config.validate(); err != nil {
		return err
	}
	if err := writeJSON(fullPath, session.Config); err != nil {
		return err
	}
	session.Path, session.Dirty = fullPath, false
	fmt.Fprintln(a.Console.Writer, "已保存配置：", fullPath)
	return nil
}
