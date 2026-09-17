package public

import "testing"

// TestSniffFaviconType 钉住 favicon 的类型判定。
//
// 背景：上传接口接受任意图片却统一存成 favicon.ico。此前 /favicon.ico 走
// c.File()，Gin 按扩展名给出 image/vnd.microsoft.icon，于是上传的 PNG 被浏览器
// 当作损坏图标丢弃——「上传成功但图标不变」。这里按内容判断，测试覆盖各常见格式
// 与无法识别的情况。
func TestSniffFaviconType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}, "image/jpeg"},
		{"gif87", []byte("GIF87a\x01\x00"), "image/gif"},
		{"gif89", []byte("GIF89a\x01\x00"), "image/gif"},
		{"webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"ico", []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00}, "image/vnd.microsoft.icon"},
		{"svg", []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), "image/svg+xml"},
		{"svg-with-decl", []byte("<?xml version=\"1.0\"?><svg></svg>"), "image/svg+xml"},
		{"unknown", []byte("not an image at all"), "application/octet-stream"},
		{"empty", []byte{}, "application/octet-stream"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniffFaviconType(tc.data); got != tc.want {
				t.Errorf("sniffFaviconType(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
