package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

/** @brief 命令行与交互菜单共用的应用服务，集中预检、密码输入和系统操作。 */
type Application struct {
	Console          *Console
	Run              func(Request, io.Writer) error
	Snapshot         func() (Snapshot, error)
	ClearCredentials func() (int, error)
}

func newApplication(input io.Reader, output io.Writer) *Application {
	return &Application{Console: newConsole(input, output), Run: runWindows, Snapshot: getSnapshot, ClearCredentials: clearRDPCredentials}
}

func (a *Application) status() error {
	state, err := a.Snapshot()
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Console.Writer, "计算机：%s\n系统：%s (Build %s)\n管理员：%t\n远程桌面：%t，端口 %d，NLA %t\n时区：%s\n界面语言：%s\n清理任务：%s\n", state.Computer, state.OS, state.Build, state.IsAdmin, state.RDPEnabled, state.Port, state.NLA, state.TimeZone, strings.Join(state.Languages, ", "), state.TaskState)
	return nil
}

func (a *Application) preflight(config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	return a.Run(Request{Action: "preflight", Config: config}, a.Console.Writer)
}

func (a *Application) apply(config Config, path string) error {
	if err := a.preflight(config); err != nil {
		return err
	}
	var password string
	if config.Password.Change {
		var err error
		password, err = a.Console.password()
		if err != nil {
			return err
		}
	}
	return a.Run(Request{Action: "apply", Config: config, ConfigPath: path, Password: password}, a.Console.Writer)
}

func (a *Application) setup(config Config, path string) error {
	if err := a.apply(config, path); err != nil {
		return err
	}
	return a.clean(Request{Action: "clean", Config: config, ConfigPath: path})
}

/** @brief 预检后逐项完成清理，某项失败仍尝试其他项目，最终汇总错误。 */
func (a *Application) clean(request Request) error {
	if err := request.Config.validate(); err != nil {
		return err
	}
	if err := a.Run(Request{Action: "validate-target"}, a.Console.Writer); err != nil {
		return err
	}
	operationErr := a.Run(request, a.Console.Writer)
	var credentialErr error
	if request.Config.Cleanup.RDPCredentials && !request.Scheduled {
		credentialErr = a.deleteCredentials()
	}
	return errors.Join(operationErr, credentialErr)
}

func (a *Application) deleteCredentials() error {
	count, err := a.ClearCredentials()
	fmt.Fprintf(a.Console.Writer, "已清理当前用户的远程桌面凭据：%d 项。\n", count)
	return err
}

func (a *Application) installSchedule(config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	if !config.Cleanup.enabled() {
		return errors.New("至少启用一项清理内容后才能安装计划任务")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	state, err := a.Snapshot()
	if err != nil {
		return err
	}
	directory := filepath.Join(os.Getenv("ProgramData"), "WinServerTool")
	if err := a.Run(Request{Action: "schedule-prepare", Config: config, Directory: directory}, a.Console.Writer); err != nil {
		return err
	}
	if err := copySelfAndConfig(executable, directory, config); err != nil {
		return err
	}
	return a.Run(Request{Action: "schedule-install", Config: config, User: &state.User, ConfigPath: filepath.Join(directory, "cleanup.json"), Executable: filepath.Join(directory, "win-server-tool.exe"), Directory: directory}, a.Console.Writer)
}

func (a *Application) removeSchedule() error {
	return a.Run(Request{Action: "schedule-remove"}, a.Console.Writer)
}

func copySelfAndConfig(executable, directory string, config Config) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	target := filepath.Join(directory, "win-server-tool.exe")
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(target)) {
		in, err := os.Open(executable)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.CreateTemp(directory, ".wsi-exe-*")
		if err != nil {
			return err
		}
		defer os.Remove(out.Name())
		if _, err = io.Copy(out, in); err == nil {
			err = out.Sync()
		}
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if err := os.Rename(out.Name(), target); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(directory, "cleanup.json"), config)
}
