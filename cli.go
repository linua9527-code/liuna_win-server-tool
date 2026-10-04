package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "WinServerTool - Windows 基础配置工具")
	fmt.Fprintln(w, "  win-server-tool                         交互菜单")
	fmt.Fprintln(w, "  win-server-tool interactive [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool init [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool status")
	fmt.Fprintln(w, "  win-server-tool preflight [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool apply [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool setup [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool clean [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool schedule install [-config config.json]")
	fmt.Fprintln(w, "  win-server-tool schedule remove")
	fmt.Fprintln(w, "参数支持 -config 与 --config 两种写法。")
}

func (a *Application) dispatch(args []string) error {
	if len(args) == 0 {
		return a.interactive(defaultConfigName)
	}
	command, rest := args[0], args[1:]
	if command == "help" || command == "-h" || command == "--help" {
		printUsage(a.Console.Writer)
		return nil
	}
	if strings.HasPrefix(command, "-") {
		return a.configCommand("interactive", args)
	}
	switch command {
	case "init", "interactive", "preflight", "apply", "setup":
		return a.configCommand(command, rest)
	case "status":
		if len(rest) > 0 {
			return errors.New("status 无需额外参数")
		}
		return a.status()
	case "clean":
		return a.cleanCommand(rest)
	case "schedule":
		return a.scheduleCommand(rest)
	default:
		return fmt.Errorf("未知命令：%s", command)
	}
}

func (a *Application) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.Console.Writer)
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("存在多余参数")
	}
	return nil
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", defaultConfigName, "配置文件路径")
}

/** @brief 未指定配置且默认文件缺失时直接使用默认值，显式指定的配置路径需存在。 */
func executionConfig(fs *flag.FlagSet, path string) (Config, error) {
	config, err := loadConfig(path)
	if !errors.Is(err, os.ErrNotExist) {
		return config, err
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicit = true
		}
	})
	if explicit {
		return config, err
	}
	return defaultConfig(), nil
}

func (a *Application) configCommand(command string, args []string) error {
	fs := a.flags(command)
	path := configFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	fullPath, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	if command == "interactive" {
		return a.interactive(fullPath)
	}
	if command == "init" {
		if _, err := os.Stat(fullPath); err == nil {
			return fmt.Errorf("配置文件已存在：%s", fullPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeJSON(fullPath, defaultConfig()); err != nil {
			return err
		}
		fmt.Fprintln(a.Console.Writer, "已创建配置文件：", fullPath)
		return nil
	}
	config, err := executionConfig(fs, fullPath)
	if err != nil {
		return err
	}
	switch command {
	case "preflight":
		return a.preflight(config)
	case "apply":
		return a.apply(config, fullPath)
	case "setup":
		return a.setup(config, fullPath)
	}
	return fmt.Errorf("未知配置命令：%s", command)
}

func (a *Application) cleanCommand(args []string) error {
	fs := a.flags("clean")
	path := configFlag(fs)
	scheduled := fs.Bool("scheduled", false, "由受限计划任务调用")
	credentialsOnly := fs.Bool("credentials-only", false, "只清理当前用户远程凭据")
	userSID := fs.String("user-sid", "", "计划任务目标用户 SID")
	userProfile := fs.String("user-profile", "", "计划任务目标用户目录")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	fullPath, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	config, err := executionConfig(fs, fullPath)
	if err != nil {
		return err
	}
	if *credentialsOnly {
		if err := a.Run(Request{Action: "validate-target"}, a.Console.Writer); err != nil {
			return err
		}
		if config.Cleanup.RDPCredentials {
			return a.deleteCredentials()
		}
		return nil
	}
	request := Request{Action: "clean", Config: config, ConfigPath: fullPath, Scheduled: *scheduled}
	if *scheduled {
		if *userSID == "" || *userProfile == "" {
			return errors.New("计划任务缺少目标用户信息")
		}
		request.User = &UserContext{SID: *userSID, Profile: *userProfile}
	}
	return a.clean(request)
}

func (a *Application) scheduleCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("请指定 schedule install 或 schedule remove")
	}
	if args[0] == "remove" {
		if len(args) != 1 {
			return errors.New("删除计划任务时无需提供配置参数")
		}
		return a.removeSchedule()
	}
	if args[0] != "install" {
		return fmt.Errorf("未知 schedule 操作：%s", args[0])
	}
	fs := a.flags("schedule install")
	path := configFlag(fs)
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	config, err := executionConfig(fs, *path)
	if err != nil {
		return err
	}
	return a.installSchedule(config)
}
