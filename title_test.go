package main

// 标题栏那一行怎么拼。它出错的方式很安静——屏幕上只是某一句没出现、或者闪一下就没——所以这里把
// 四种组合逐一钉住，尤其是"上游还没问到"那一种。

import "testing"

func TestWindowTitle(t *testing.T) {
	const (
		local  = "bdb0765c5579c430cbe1ef79cf3d62831bb062a7"
		other  = "8f3a1c2de0000000000000000000000000000000"
		shortL = "bdb0765"
		shortO = "8f3a1c2"
	)
	for _, c := range []struct {
		name   string
		local  string
		remote string
		known  bool
		want   string
	}{
		{
			name: "本机 hash 还没有：只写程序名",
			want: appName,
		},
		{
			name:  "上游还没问到：不宣称新版",
			local: local,
			want:  appName + " - " + shortL,
		},
		{
			name:   "问到了，就是同一版",
			local:  local,
			remote: local,
			known:  true,
			want:   appName + " - " + shortL,
		},
		{
			name:   "问到了，不一样",
			local:  local,
			remote: other,
			known:  true,
			want:   appName + " - " + shortL + " - 检测到新版本 " + shortO,
		},
		{
			// 问到了，但那次问到的正是"空"（理论上不该发生）：也不该说新版。
			name:  "问到了，却是个空 hash",
			local: local,
			known: true,
			want:  appName + " - " + shortL,
		},
	} {
		if got := windowTitle(c.local, c.remote, c.known); got != c.want {
			t.Errorf("%s: windowTitle = %q, want %q", c.name, got, c.want)
		}
	}
}
