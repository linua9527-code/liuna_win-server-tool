package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var errConsoleClosed = errors.New("输入结束")

/** @brief 共用控制台输入缓冲区，避免菜单和密码输入争用标准输入。 */
type Console struct {
	Reader *bufio.Reader
	Writer io.Writer
	Secret func(*bufio.Reader, io.Writer, string) (string, error)
}

func newConsole(input io.Reader, output io.Writer) *Console {
	return &Console{Reader: bufio.NewReader(input), Writer: output, Secret: readSecret}
}

func (c *Console) line(prompt string) (string, error) {
	fmt.Fprint(c.Writer, prompt)
	line, err := c.Reader.ReadString('\n')
	if err == io.EOF && len(line) > 0 {
		err = nil
	} else if errors.Is(err, io.EOF) {
		err = errConsoleClosed
	}
	return strings.TrimSpace(line), err
}

func (c *Console) text(label, current string) (string, error) {
	value, err := c.line(fmt.Sprintf("%s [%s]：", label, current))
	if err != nil {
		return "", err
	}
	if value == "" {
		return current, nil
	}
	return value, nil
}

func (c *Console) optionalText(label, current string) (string, error) {
	value, err := c.text(label+"（- 清空）", current)
	if value == "-" {
		value = ""
	}
	return value, err
}

func (c *Console) boolean(label string, current bool) (bool, error) {
	defaultChoice := "y/N"
	if current {
		defaultChoice = "Y/n"
	}
	for {
		value, err := c.line(fmt.Sprintf("%s [%s]：", label, defaultChoice))
		if err != nil {
			return false, err
		}
		switch strings.ToLower(value) {
		case "":
			return current, nil
		case "y", "yes", "是":
			return true, nil
		case "n", "no", "否":
			return false, nil
		default:
			fmt.Fprintln(c.Writer, "请输入 y 或 n。")
		}
	}
}

func (c *Console) integer(label string, current, minimum, maximum int) (int, error) {
	for {
		value, err := c.text(label, strconv.Itoa(current))
		if err != nil {
			return 0, err
		}
		number, err := strconv.Atoi(value)
		if err == nil && number >= minimum && number <= maximum {
			return number, nil
		}
		fmt.Fprintf(c.Writer, "请输入 %d 至 %d 的整数。\n", minimum, maximum)
	}
}

func (c *Console) password() (string, error) {
	password, err := c.Secret(c.Reader, c.Writer, "新密码：")
	if errors.Is(err, io.EOF) {
		err = errConsoleClosed
	}
	if err != nil {
		return "", err
	}
	if len([]rune(password)) < 12 {
		return "", fmt.Errorf("密码至少需要 12 个字符")
	}
	confirmation, err := c.Secret(c.Reader, c.Writer, "再次输入：")
	if errors.Is(err, io.EOF) {
		err = errConsoleClosed
	}
	if err != nil {
		return "", err
	}
	if password != confirmation {
		return "", fmt.Errorf("两次输入的密码不一致")
	}
	return password, nil
}
