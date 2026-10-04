package main

// 客户端目录那几件小事：记住、读回来、以及"像不像"。选择框本身没法在测试里点，所以它单独放在最后，
// 而且默认不跑。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndReadClientFolder(t *testing.T) {
	withDataDir(t)

	// 还没选过：不是错误，只是"不知道"。
	if got := savedClientFolder(); got != "" {
		t.Fatalf("什么都还没选时 savedClientFolder() = %q，want 空", got)
	}
	// 空路径没什么可记的，也不必报错。
	if err := saveClientFolder("  "); err != nil {
		t.Fatalf("saveClientFolder(空) 不该报错：%v", err)
	}

	const folder = `D:\Arknights\Arknights_Data\StreamingAssets\AB\Windows`
	if err := saveClientFolder(folder); err != nil {
		t.Fatalf("saveClientFolder: %v", err)
	}
	if got := savedClientFolder(); got != folder {
		t.Fatalf("savedClientFolder() = %q, want %q", got, folder)
	}

	// 文件坏了：也只当"不知道"，不崩、不编一个路径出来。
	path := filepath.Join(dataDirOverride, "client-folder.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := savedClientFolder(); got != "" {
		t.Errorf("坏文件时 savedClientFolder() = %q，want 空", got)
	}
}

func TestClientFolderLooksRight(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{`D:\Arknights\Arknights_Data\StreamingAssets\AB\Windows`, true},
		{`C:/Program Files/Hypergryph Launcher/games/Arknights/Arknights_Data/StreamingAssets/AB/Windows`, true},
		{`C:\Arknights\Arknights_Data\StreamingAssets\AB`, true}, // 认到 arknights 那一段也算
		{`C:\Users\me\Desktop`, false},
		{`C:\Program Files`, false},
		{"", false},
		{"   ", false},
	} {
		if got := clientFolderLooksRight(c.path); got != c.want {
			t.Errorf("clientFolderLooksRight(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// 选择框会挡住测试直到有人点，所以默认跳过。要看它长什么样：
//
//	go test -v -run TestChooseFolder .
func TestChooseFolder(t *testing.T) {
	if os.Getenv("SHOW_FOLDER_PICKER") == "" && !testing.Verbose() {
		t.Skip("会弹一个真的文件夹选择框：用 -v 或 SHOW_FOLDER_PICKER=1 才跑")
	}
	// 0 = 没有属主。测试里本来就没有主窗口，而这正好也验一下"没有属主也还能弹"这条退路。
	folder, ok := chooseFolder(0, "测试：选择客户端目录", "")
	t.Logf("选了 %q，ok=%v", folder, ok)
}

// 选回来那串字符的清理。三件都真见过：粘贴时带来的引号、末尾的反斜杠、以及正斜杠。
func TestNormalizePickedFolder(t *testing.T) {
	sep := string(rune(92))
	q := quote
	build := func(parts ...string) string { return strings.Join(parts, sep) }
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "粘贴时带来的引号",
			in:   q + build("E:", "Arknights", "AB", "Windows") + q,
			want: build("E:", "Arknights", "AB", "Windows"),
		},
		{
			name: "正斜杠",
			in:   "E:/Arknights/AB/Windows",
			want: build("E:", "Arknights", "AB", "Windows"),
		},
		{
			name: "前后空白 + 末尾多一个分隔符",
			in:   "  " + build("E:", "AB") + sep + "  ",
			want: build("E:", "AB"),
		},
		{
			name: "盘根那一个要留着",
			in:   "E:" + sep,
			want: "E:" + sep,
		},
		{
			name: "两个分隔符收成一个（盘根）",
			in:   "C:" + sep + sep,
			want: "C:" + sep,
		},
	} {
		if got := normalizePickedFolder(c.in); got != c.want {
			t.Errorf("%s：normalizePickedFolder(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
