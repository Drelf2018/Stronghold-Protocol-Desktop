//go:build windows

package main

// What integrity level this process is running at, and what that implies.
//
// It is worth measuring because it decides what the process may write, independently of the ACLs:
// a low-integrity process cannot write anywhere in the user profile at all - not %LOCALAPPDATA%,
// not even %TEMP%, because those are medium - and WebView2 cannot create its user data folder
// either. A sandbox, a restricted token, or a launcher that hands one out is what puts a process
// at that level.
//
// That combination is the confusing one: "Access is denied" from a directory whose ACL grants
// FullControl. This is the missing half of that explanation, and it costs one log line to have.

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processIntegrity names the level the current process runs at: "untrusted", "low", "medium",
// "high", "system", or "unknown" when the token cannot be asked.
func processIntegrity() string {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "unknown"
	}
	defer token.Close()

	buf := make([]byte, 256)
	var size uint32
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &size); err != nil {
		return "unknown"
	}
	// TOKEN_MANDATORY_LABEL { SID_AND_ATTRIBUTES Label }: the header holds a pointer to the SID,
	// and the SID itself sits after it inside the same buffer.
	label := (*windows.SIDAndAttributes)(unsafe.Pointer(&buf[0]))
	sid := label.Sid
	if sid == nil || sid.SubAuthorityCount() == 0 {
		return "unknown"
	}
	switch rid := sid.SubAuthority(uint32(sid.SubAuthorityCount() - 1)); {
	case rid >= 0x4000:
		return "system"
	case rid >= 0x3000:
		return "high"
	case rid >= 0x2000:
		return "medium"
	case rid >= 0x1000:
		return "low"
	default:
		return "untrusted"
	}
}

// integrityNote is the sentence a message box adds: the level itself, and - when it is one that
// cannot write the user's own profile - what that means and what to do about it. Neither of the
// levels below is what a person gets by double-clicking, so naming the cause here is the whole
// difference between a mysterious failure and an actionable one.
func integrityNote(level string) string {
	if level == "low" || level == "untrusted" {
		return "\n\n本进程的完整性级别是 " + level + "：这个级别的进程写不进用户目录（%LOCALAPPDATA%、%TEMP% 都是 medium），" +
			"WebView2 也就建不出自己的数据目录。它通常意味着本程序是被沙箱或受限环境拉起来的——" +
			"请在资源管理器里直接双击它，而不是从别的应用里的链接打开。"
	}
	return "\n\n本进程的完整性级别：" + level
}
