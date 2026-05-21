package tunnel

import "testing"

func TestRemote_Validate(t *testing.T) {
	cases := []struct {
		name    string
		r       Remote
		wantErr bool
	}{
		{"tcp 合法", Remote{PublicPort: 40001, LocalAddr: "127.0.0.1:22", Protocol: "tcp"}, false},
		{"udp 合法", Remote{PublicPort: 40002, LocalAddr: "127.0.0.1:53", Protocol: "UDP"}, false},
		{"端口为零", Remote{PublicPort: 0, LocalAddr: "127.0.0.1:22", Protocol: "tcp"}, true},
		{"端口越界", Remote{PublicPort: 99999, LocalAddr: "127.0.0.1:22", Protocol: "tcp"}, true},
		{"空 local_addr", Remote{PublicPort: 40001, Protocol: "tcp"}, true},
		{"非法 protocol", Remote{PublicPort: 40001, LocalAddr: "x:1", Protocol: "sctp"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.r.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestRemote_ToChiselSpec(t *testing.T) {
	cases := []struct {
		r    Remote
		want string
	}{
		{Remote{PublicPort: 40001, LocalAddr: "127.0.0.1:22", Protocol: "tcp"}, "R:0.0.0.0:40001:127.0.0.1:22"},
		{Remote{PublicPort: 40002, LocalAddr: "127.0.0.1:53", Protocol: "udp"}, "R:0.0.0.0:40002:127.0.0.1:53/udp"},
		{Remote{PublicPort: 40003, LocalAddr: "127.0.0.1:80", Protocol: "TCP"}, "R:0.0.0.0:40003:127.0.0.1:80"},
	}
	for _, c := range cases {
		if got := c.r.ToChiselSpec(); got != c.want {
			t.Errorf("ToChiselSpec(%+v)=%q want %q", c.r, got, c.want)
		}
	}
}

func TestLocal_ValidateAndSpec(t *testing.T) {
	good := Local{LocalPort: 8022, RemotePublicPort: 41001, Protocol: "tcp"}
	if err := good.Validate(); err != nil {
		t.Errorf("合法 Local 报错: %v", err)
	}
	if got := good.ToChiselSpec(); got != "127.0.0.1:8022:127.0.0.1:41001" {
		t.Errorf("spec 错: %s", got)
	}
	udp := Local{LocalPort: 53, RemotePublicPort: 41053, Protocol: "udp"}
	if got := udp.ToChiselSpec(); got != "127.0.0.1:53:127.0.0.1:41053/udp" {
		t.Errorf("udp spec 错: %s", got)
	}

	bad := []Local{
		{LocalPort: 0, RemotePublicPort: 1, Protocol: "tcp"},
		{LocalPort: 1, RemotePublicPort: 70000, Protocol: "tcp"},
		{LocalPort: 1, RemotePublicPort: 1, Protocol: "sctp"},
	}
	for _, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("应失败: %+v", b)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort string
		wantErr  bool
	}{
		{":8443", "0.0.0.0", "8443", false},
		{"127.0.0.1:8443", "127.0.0.1", "8443", false},
		{"bad-no-port", "", "", true},
		{":notnum", "", "", true},
	}
	for _, c := range cases {
		h, p, err := splitHostPort(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("splitHostPort(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && (h != c.wantHost || p != c.wantPort) {
			t.Errorf("splitHostPort(%q)=(%s,%s) want (%s,%s)", c.in, h, p, c.wantHost, c.wantPort)
		}
	}
}
