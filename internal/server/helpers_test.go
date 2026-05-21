package server

import "testing"

func TestNormalizeLocalAddr(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		// 纯端口号 → 默认 127.0.0.1
		{"22", "127.0.0.1:22", false},
		{"8080", "127.0.0.1:8080", false},
		{"65535", "127.0.0.1:65535", false},
		// ":port" 形式
		{":22", "127.0.0.1:22", false},
		{":8080", "127.0.0.1:8080", false},
		// 完整 host:port 原样
		{"127.0.0.1:22", "127.0.0.1:22", false},
		{"192.168.1.10:8096", "192.168.1.10:8096", false},
		{"localhost:80", "localhost:80", false},
		{"my-nas.local:445", "my-nas.local:445", false},
		// IPv6 字面量
		{"[::1]:22", "[::1]:22", false},
		{"[fe80::1]:80", "[fe80::1]:80", false},
		// trim 空白
		{"  22  ", "127.0.0.1:22", false},
		// 错误：空字符串
		{"", "", true},
		{"   ", "", true},
		// 错误：端口越界
		{"0", "", true},
		{"65536", "", true},
		{"-1", "", true},
		// 错误：端口非数字
		{":abc", "", true},
		{"host:abc", "", true},
		// 错误：没有端口
		{"127.0.0.1", "", true},
		{"hostname", "", true},
	}
	for _, c := range cases {
		got, err := normalizeLocalAddr(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("normalizeLocalAddr(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("normalizeLocalAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
