package utils

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"hosting/internal/global"
)

// SplitFileIDURL 从 /file/ 直链中切分 fileId 与文件名，与 BuildFileIDURL 互逆。
// 输入为路径段（{fileId}.{filename}），完整路径（/file/{fileId}.{filename}）亦可正确处理。
// 规则：最后一个 "/" 之后的段中，第一个 "." 之前为 fileId（Telegram file_id 字符集
// [A-Za-z0-9_-]，不含 "."），之后为文件名（允许为空，此时调用方需按内容检测判定类型）。
func SplitFileIDURL(pathSegment string) (fileID, filename string, err error) {
	if idx := strings.LastIndex(pathSegment, "/"); idx >= 0 {
		pathSegment = pathSegment[idx+1:]
	}
	idx := strings.Index(pathSegment, ".")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid file URL: missing '.' separator")
	}
	fileID = pathSegment[:idx]
	if fileID == "" {
		return "", "", fmt.Errorf("invalid file URL: empty fileId")
	}
	return fileID, pathSegment[idx+1:], nil
}

// BuildFileIDURL 构造 fileId 直链路径：/file/{fileId}.{urlEscape(filename)}
func BuildFileIDURL(fileID, filename string) string {
	return fmt.Sprintf("/file/%s.%s", fileID, url.PathEscape(filename))
}

func GetFileExtension(mimeType string) (string, bool) {
	ext, ok := global.AllowedMimeTypes[mimeType]
	return ext, ok
}

// GetFileCategory 判断文件是图片还是文档
func GetFileCategory(mimeType string) string {
	if strings.HasPrefix(mimeType, "image/") {
		return "image"
	}
	return "document"
}

func NormalizeFileExtension(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".jpeg" {
		return ".jpg"
	}
	return ext
}

func ValidateIPAddress(ip string) string {
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if net.ParseIP(ip) == nil {
		return "unknown"
	}
	return ip
}

func SanitizeUserAgent(ua string) string {
	reg := regexp.MustCompile(`[^\w\s\-\.,;:/\(\)]`)
	ua = reg.ReplaceAllString(ua, "")
	if len(ua) > 255 {
		ua = ua[:255]
	}
	return ua
}

// SanitizeFilename 清理文件名，移除 Windows 不允许的字符
func SanitizeFilename(filename string) string {
	filename = filepath.Base(filename)
	reg := regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	filename = reg.ReplaceAllString(filename, "")
	if filename == "" || filename == "." {
		now := time.Now()
		filename = fmt.Sprintf("image_%s", now.Format("20060102150405"))
	}

	// 限制文件名长度（Windows 限制 255 字符）
	if len([]rune(filename)) > 250 {
		ext := filepath.Ext(filename)
		runes := []rune(filename)
		// 保留扩展名
		if len(ext) > 0 {
			filename = string(runes[:250-len([]rune(ext))]) + ext
		} else {
			filename = string(runes[:250])
		}
	}
	return filename
}

// CleanFilenameForWindows 清理文件名以适应 Windows 文件系统
func CleanFilenameForWindows(filename string) string {
	// 替换 Windows 不允许的字符
	windowsInvalidChars := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
	for _, char := range windowsInvalidChars {
		filename = strings.ReplaceAll(filename, char, "_")
	}

	// 移除控制字符
	reg := regexp.MustCompile(`[\x00-\x1f]`)
	filename = reg.ReplaceAllString(filename, "")

	// 限制文件名长度
	if len([]rune(filename)) > 250 {
		ext := filepath.Ext(filename)
		runes := []rune(filename)
		if len(ext) > 0 {
			filename = string(runes[:250-len([]rune(ext))]) + ext
		} else {
			filename = string(runes[:250])
		}
	}

	// 确保文件名不为空
	if filename == "" || filename == "." {
		now := time.Now()
		filename = fmt.Sprintf("file_%s", now.Format("20060102150405"))
	}

	return filename
}

func GetPageTitle(page string) string {
	return fmt.Sprintf("%s | %s", page, global.AppConfig.Site.Name)
}
