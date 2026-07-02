package piagent

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Skill 对应 OpenClaw SKILL.md 规范：指令包，非可执行工具
// 参考 https://docs.openclaw.ai/tools/skills
type Skill struct {
	Name          string `json:"name" yaml:"name"`
	Description   string `json:"description" yaml:"description"`
	Instruction   string `json:"instruction" yaml:"-"`   // markdown body
	Metadata      string `json:"metadata" yaml:"-"`      // JSON string
	UserInvocable bool   `json:"user_invocable" yaml:"user-invocable"`
	Enabled       bool   `json:"enabled" yaml:"x-enabled"`
	Source        string `json:"source" yaml:"-"`
}

// SkillsDir 返回项目根下的 skills 目录
func SkillsDir() string {
	// 从 CWD 或二进制所在目录推断
	if d, err := os.Getwd(); err == nil {
		if fi, err := os.Stat(filepath.Join(d, "skills")); err == nil && fi.IsDir() {
			return filepath.Join(d, "skills")
		}
	}
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(filepath.Join(filepath.Dir(exe), "skills")); err == nil && fi.IsDir() {
			return filepath.Join(filepath.Dir(exe), "skills")
		}
	}
	return "skills"
}

// LoadSkillsFromDir 扫描 skills 目录，加载所有 SKILL.md
func LoadSkillsFromDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 skills 目录失败: %w", err)
	}
	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		skillDir := filepath.Join(dir, e.Name())
		s, err := ReadSkillFromDir(skillDir)
		if err != nil {
			log.Printf("[Skill] 跳过 %s: %v", e.Name(), err)
			continue
		}
		skills = append(skills, *s)
	}
	return skills, nil
}

// ReadSkillFromDir 读取一个 skills/<name>/SKILL.md 目录
func ReadSkillFromDir(skillDir string) (*Skill, error) {
	mdPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, fmt.Errorf("读取 SKILL.md 失败: %w", err)
	}
	return ParseSKILLMd(string(data))
}

// ParseSKILLMd 解析 SKILL.md 文本（frontmatter + body）
func ParseSKILLMd(content string) (*Skill, error) {
	// 提取 frontmatter
	body := content
	var fmLines []string
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		rest := strings.TrimSpace(content)[3:]
		end := strings.Index(rest, "\n---")
		if end >= 0 {
			fmLines = strings.Split(strings.TrimSpace(rest[:end]), "\n")
			body = strings.TrimSpace(rest[end+4:])
		}
	}

	s := &Skill{
		Enabled: true,
		Source:  "workspace",
	}
	for _, line := range fmLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		val = strings.Trim(val, "\"'")
		switch key {
		case "name":
			s.Name = val
		case "description":
			s.Description = val
		case "user-invocable":
			s.UserInvocable = val == "true"
		case "x-enabled":
			s.Enabled = val == "true"
		case "metadata":
			s.Metadata = val
		}
	}

	if s.Name == "" {
		return nil, fmt.Errorf("SKILL.md 缺少 name")
	}
	s.Instruction = body
	return s, nil
}

// WriteSkillToDir 写入 skills/<name>/SKILL.md
func WriteSkillToDir(baseDir string, s Skill) error {
	dir := filepath.Join(baseDir, s.Name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建技能目录失败: %w", err)
	}
	md, err := BuildSKILLMd(s)
	if err != nil {
		return err
	}
	mdPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}
	return nil
}

// DeleteSkillFromDir 删除 skills/<name>/ 目录
func DeleteSkillFromDir(baseDir string, name string) error {
	dir := filepath.Join(baseDir, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("技能 %s 不存在", name)
	}
	return os.RemoveAll(dir)
}

// BuildSKILLMd 从 Skill 生成 SKILL.md 格式文本
func BuildSKILLMd(s Skill) (string, error) {
	meta := s.Metadata
	if meta == "" {
		meta = "{}"
	}
	var metaObj interface{}
	if err := json.Unmarshal([]byte(meta), &metaObj); err != nil {
		return "", fmt.Errorf("metadata 不是合法 JSON: %w", err)
	}
	metaBytes, _ := json.Marshal(metaObj)

	enabled := "true"
	if !s.Enabled {
		enabled = "false"
	}
	ui := "true"
	if !s.UserInvocable {
		ui = "false"
	}

	return fmt.Sprintf(`---
name: %s
description: %s
user-invocable: %s
x-enabled: %s
metadata: %s
---

%s`, s.Name, s.Description, ui, enabled, string(metaBytes), s.Instruction), nil
}

// BuildSkillsPrompt 将所有启用的 Skill 编译为 XML 块注入系统提示词
func BuildSkillsPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n## 可用 Skill 指令\n")
	b.WriteString("以下是预定义的工作流程指令。当用户请求匹配以下某个 Skill 时，")
	b.WriteString("严格按照其指令逐步执行，使用已有工具完成。\n\n")

	for _, s := range skills {
		if !s.Enabled {
			continue
		}
		b.WriteString(fmt.Sprintf("### %s\n", s.Name))
		if s.Description != "" {
			b.WriteString(fmt.Sprintf("> %s\n\n", s.Description))
		}
		b.WriteString(s.Instruction)
		b.WriteString("\n\n")
	}

	// 使用汇报要求 —— 放在最后利用近因效应，AI 更容易遵守
	b.WriteString("---\n\n")
	b.WriteString("## ⚠️ Skill 使用汇报（强制要求）\n\n")
	b.WriteString("当你决定采用上述某个 Skill 的工作流来回答用户时，")
	b.WriteString("**必须**在你最终回复的**第一行**输出以下标记（每使用一个 Skill 输出一行）：\n\n")
	b.WriteString("```\n<used-skill>skill-name</used-skill>\n```\n\n")
	b.WriteString("例如你使用 `disk-check` 完成了磁盘检查，则回复应以：\n\n")
	b.WriteString("```\n<used-skill>disk-check</used-skill>\n\n然后是你正常的回复内容...\n```\n\n")
	b.WriteString("这个标记会被系统过滤，用户看不到。用于内部统计。")
	b.WriteString("如果没有使用任何 Skill（例如只是简单问答），则**不要**输出此标记。\n\n")
	return b.String()
}
