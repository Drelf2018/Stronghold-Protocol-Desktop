package main

// The injected mute script and the two lines of Go that call into it have to agree on the name.
// A rename on one side is an error nowhere: it is sound that keeps playing when the window goes
// away, or a game that stays silent after it comes back. The same check used to guard the
// notification script this program no longer has.

import (
	"strings"
	"testing"
)

func TestMuteScript(t *testing.T) {
	if !strings.Contains(muteJS, "window._mutePage") {
		t.Error("js/mute.js does not define window._mutePage, so nothing Go evaluates can work")
	}
	if !strings.Contains(muteJS, "AudioContext") {
		t.Error("js/mute.js never touches AudioContext: the game's sound would keep playing")
	}
	// 只挂起不恢复，就是"关过一次之后永远没声音"；只恢复不挂起则是白跑。
	for _, want := range []string{"suspend", "resume"} {
		if !strings.Contains(muteJS, want) {
			t.Errorf("js/mute.js never calls %s", want)
		}
	}
	for _, call := range []string{mutePageOn, mutePageOff} {
		if !strings.Contains(call, "_mutePage") {
			t.Errorf("脚本 Go 实际执行的是 %q，里面没有 _mutePage", call)
		}
	}
	if !strings.Contains(mutePageOn, "true") || !strings.Contains(mutePageOff, "false") {
		t.Errorf("两个调用没有区分开关：%q / %q", mutePageOn, mutePageOff)
	}
}
