package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	RDP      RDPConfig      `json:"rdp"`
	TimeZone string         `json:"time_zone"`
	Language LanguageConfig `json:"language"`
	Password PasswordConfig `json:"password"`
	Cleanup  CleanupConfig  `json:"cleanup"`
	Schedule ScheduleConfig `json:"schedule"`
}

type RDPConfig struct {
	Enabled         bool     `json:"enabled"`
	Port            int      `json:"port"`
	RequireNLA      bool     `json:"require_nla"`
	RemoteAddresses []string `json:"remote_addresses"`
	Lockout         bool     `json:"configure_lockout"`
	Threshold       int      `json:"lockout_threshold"`
	DurationMinutes int      `json:"lockout_duration_minutes"`
	ResetMinutes    int      `json:"lockout_reset_minutes"`
}

type LanguageConfig struct {
	Enabled          bool   `json:"enabled"`
	Tag              string `json:"tag"`
	InstallIfMissing bool   `json:"install_if_missing"`
	PackageCAB       string `json:"package_cab"`
	CopyToSystem     bool   `json:"copy_to_system"`
}

type PasswordConfig struct {
	Change   bool   `json:"change"`
	Username string `json:"username"`
}

type CleanupConfig struct {
	RDPHistory     bool `json:"rdp_history"`
	RDPCredentials bool `json:"rdp_credentials"`
	RecentFiles    bool `json:"recent_files"`
	TempFiles      bool `json:"temp_files"`
	EventLogs      bool `json:"event_logs"`
}

type ScheduleConfig struct {
	DailyAt string `json:"daily_at"`
}

type UserContext struct {
	SID     string `json:"sid"`
	Profile string `json:"profile"`
}

type ScheduledConfig struct {
	Config Config      `json:"config"`
	User   UserContext `json:"user"`
}

func defaultConfig() Config {
	return Config{
		RDP: RDPConfig{Enabled: true, Port: 3389, RequireNLA: true,
			RemoteAddresses: []string{"Any"}, Lockout: true, Threshold: 5, DurationMinutes: 15, ResetMinutes: 15},
		TimeZone: "China Standard Time",
		Language: LanguageConfig{Enabled: true, Tag: "zh-CN"},
		Cleanup: CleanupConfig{
			RDPHistory: true, RDPCredentials: true, RecentFiles: true,
			TempFiles: true, EventLogs: true,
		},
		Schedule: ScheduleConfig{DailyAt: "03:00"},
	}
}

func (c Config) validate() error {
	if c.RDP.Enabled {
		if c.RDP.Port < 1 || c.RDP.Port > 65535 {
			return errors.New("远程桌面端口应为 1 至 65535")
		}
		if len(c.RDP.RemoteAddresses) == 0 {
			return errors.New("请指定防火墙允许的来源地址")
		}
		for _, address := range c.RDP.RemoteAddresses {
			if address == "Any" || address == "LocalSubnet" {
				continue
			}
			if _, err := netip.ParseAddr(address); err == nil {
				continue
			}
			if _, err := netip.ParsePrefix(address); err == nil {
				continue
			}
			return fmt.Errorf("防火墙来源地址格式错误：%s", address)
		}
	}
	if c.RDP.Lockout {
		if c.RDP.Threshold < 1 || c.RDP.Threshold > 999 {
			return errors.New("登录失败锁定阈值应为 1 至 999")
		}
		if c.RDP.DurationMinutes < 1 || c.RDP.DurationMinutes > 99999 ||
			c.RDP.ResetMinutes < 1 || c.RDP.ResetMinutes > c.RDP.DurationMinutes {
			return errors.New("锁定时间应为 1 至 99999 分钟，重置计数时间应为 1 至锁定时间")
		}
	}
	if c.Language.Enabled && !regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})+$`).MatchString(c.Language.Tag) {
		return errors.New("语言标识格式错误，例如 zh-CN")
	}
	if c.Language.PackageCAB != "" && (!filepath.IsAbs(c.Language.PackageCAB) || !strings.EqualFold(filepath.Ext(c.Language.PackageCAB), ".cab")) {
		return errors.New("语言包应为本机 CAB 文件的绝对路径")
	}
	if _, err := time.Parse("15:04", c.Schedule.DailyAt); err != nil || len(c.Schedule.DailyAt) != 5 {
		return errors.New("定时清理时间应为 HH:mm，例如 03:00")
	}
	return nil
}

func (c CleanupConfig) enabled() bool {
	return c.RDPHistory || c.RDPCredentials || c.RecentFiles || c.TempFiles || c.EventLogs
}

func decodeJSON(r io.Reader, target any) error {
	d := json.NewDecoder(io.LimitReader(r, 1024*1024+1))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("配置文件应只有一个 JSON 对象")
	}
	return nil
}

func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	if err := decodeJSON(f, &c); err != nil {
		return c, fmt.Errorf("读取配置：%w", err)
	}
	return c, c.validate()
}

/** @brief 使用同目录临时文件替换目标，避免中断留下半份配置。 */
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".wsi-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0600)
}
