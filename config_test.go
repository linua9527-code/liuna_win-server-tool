package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"端口超范围", func(c *Config) { c.RDP.Port = 65536 }},
		{"来源地址错误", func(c *Config) { c.RDP.RemoteAddresses = []string{"10.0.0.1; command"} }},
		{"缺少来源地址", func(c *Config) { c.RDP.RemoteAddresses = nil }},
		{"锁定周期冲突", func(c *Config) { c.RDP.ResetMinutes = c.RDP.DurationMinutes + 1 }},
		{"语言注入", func(c *Config) { c.Language.Tag = "zh-CN'; command" }},
		{"时间超范围", func(c *Config) { c.Schedule.DailyAt = "24:00" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := defaultConfig()
			test.mutate(&c)
			if err := c.validate(); err == nil {
				t.Fatal("危险或无效配置被接受")
			}
		})
	}
	c := defaultConfig()
	c.RDP.RemoteAddresses = []string{"192.0.2.10", "192.0.2.0/24", "2001:db8::/32", "LocalSubnet"}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStrictJSON(t *testing.T) {
	for _, value := range []string{`{"unknown":1}`, `{} {}`, `{"rdp": {"porrt":3389}}`, `{"cleanup":{"temp_min_age_days":7}}`} {
		var config Config
		if err := decodeJSON(strings.NewReader(value), &config); err == nil {
			t.Fatalf("接受错误配置：%s", value)
		}
	}
}

func TestAllCleanupOptionsEnabledByDefault(t *testing.T) {
	expected := CleanupConfig{RDPHistory: true, RDPCredentials: true, RecentFiles: true, TempFiles: true, EventLogs: true}
	if defaultConfig().Cleanup != expected {
		t.Fatal("默认配置未开启全部清理项")
	}
	config, err := loadConfig("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if config.Cleanup != expected {
		t.Fatal("示例配置未开启全部清理项")
	}
}

func TestConfigDefaultsAndSecretRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"rdp":{"port":3390}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.RDP.Port != 3390 || !c.RDP.RequireNLA || c.TimeZone != "China Standard Time" {
		t.Fatal("配置缺省值丢失")
	}
	if err := os.WriteFile(path, []byte(`{"password":{"secret":"test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("接受了持久化明文密码")
	}
}
