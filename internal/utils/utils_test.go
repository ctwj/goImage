package utils

import (
	"net/url"
	"testing"
)

// TestSplitFileIDURL 覆盖 SplitFileIDURL 的正常与边界输入（tasks.md T008 / research.md D1）
func TestSplitFileIDURL(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantFileID  string
		wantName    string
		wantErr     bool
	}{
		{"普通 jpg", "AgACAgUAAx0CPQAB1w.photo.jpg", "AgACAgUAAx0CPQAB1w", "photo.jpg", false},
		{"含下划线和连字符的 fileId", "BQACAgIAAyE-AAIBCgabc.test.png", "BQACAgIAAyE-AAIBCgabc", "test.png", false},
		{"空文件名", "AgACAgUAAx0CPQAB1w.", "AgACAgUAAx0CPQAB1w", "", false},
		{"文件名含多级点", "FILEID123.a.b.png", "FILEID123", "a.b.png", false},
		{"URL 编码的中文文件名", "FILEID123.%E5%9B%BE%E7%89%87.png", "FILEID123", "%E5%9B%BE%E7%89%87.png", false},
		{"无分隔符", "FILEID123-nodot_png", "", "", true},
		{"fileId 为空", ".hidden.png", "", "", true},
		{"仅分隔符", ".", "", "", true},
		{"带前导斜杠（防御）", "/FILEID123.a.png", "FILEID123", "a.png", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileID, filename, err := SplitFileIDURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SplitFileIDURL(%q) 期望出错，实际返回 fileID=%q filename=%q", tc.input, fileID, filename)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitFileIDURL(%q) 意外出错: %v", tc.input, err)
			}
			if fileID != tc.wantFileID {
				t.Errorf("fileID = %q, 期望 %q", fileID, tc.wantFileID)
			}
			if filename != tc.wantName {
				t.Errorf("filename = %q, 期望 %q", filename, tc.wantName)
			}
		})
	}
}

// TestBuildFileIDURL 验证链接构造的格式与编码
func TestBuildFileIDURL(t *testing.T) {
	cases := []struct {
		name     string
		fileID   string
		filename string
		want     string
	}{
		{"普通文件", "FILEID123", "a.png", "/file/FILEID123.a.png"},
		{"中文文件名被编码", "FILEID123", "图片.png", "/file/FILEID123." + url.PathEscape("图片.png")},
		{"空文件名", "FILEID123", "", "/file/FILEID123."},
		{"特殊字符文件名", "FILEID123", "a b&c.png", "/file/FILEID123." + url.PathEscape("a b&c.png")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildFileIDURL(tc.fileID, tc.filename)
			if got != tc.want {
				t.Errorf("BuildFileIDURL(%q, %q) = %q, 期望 %q", tc.fileID, tc.filename, got, tc.want)
			}
		})
	}
}

// TestBuildSplitRoundtrip 验证构造与切分互逆
func TestBuildSplitRoundtrip(t *testing.T) {
	inputs := []struct{ fileID, filename string }{
		{"AgACAgUAAx0CPQAB1w", "photo.jpg"},
		{"BQAC-A_bCdEf", "a.b.c.png"},
		{"FILEID123", "图片 测试.png"},
		{"FILEID123", ""},
		{"ZyXwVuTs", "weird name (1) & [2].webp"},
	}

	for _, in := range inputs {
		path := BuildFileIDURL(in.fileID, in.filename)
		gotID, gotName, err := SplitFileIDURL(path)
		if err != nil {
			t.Fatalf("roundtrip: SplitFileIDURL(%q) 出错: %v", path, err)
		}
		// 文件名经过 PathEscape，切分后需还原比较
		decodedName, err := url.PathUnescape(gotName)
		if err != nil {
			t.Fatalf("roundtrip: PathUnescape(%q) 出错: %v", gotName, err)
		}
		if gotID != in.fileID {
			t.Errorf("roundtrip: fileID = %q, 期望 %q (path=%q)", gotID, in.fileID, path)
		}
		if decodedName != in.filename {
			t.Errorf("roundtrip: filename = %q, 期望 %q (path=%q)", decodedName, in.filename, path)
		}
	}
}
