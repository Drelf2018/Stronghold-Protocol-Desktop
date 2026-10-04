//go:build windows

package main

// 本地客户端那个目录记在哪里。
//
// 与 game-source.json 同一个数据目录、同一套写法：这是本程序自己的账，不放进 game\（那里的一切都是
// 上游的东西，覆盖式解包会把它清掉）。
//
// 记它是为了那一次选择只用做一次：装在别的盘、别的目录的人，不该每次提取都被问一遍。

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// clientFilePath 是选择框选中的那个目录记在哪里。
func clientFilePath() string { return filepath.Join(appDataDir(), "client-folder.json") }

// clientFolder 是那个文件的内容。时间戳只为读日志的人留着：出问题时"这是什么时候选的"往往比路径
// 本身更有用（那条路径后来可能被删了或者移了）。
type clientFolder struct {
	Path     string    `json:"path"`
	ChosenAt time.Time `json:"chosenAt"`
}

// savedClientFolder 是上一次记住的那个目录；没有时给空串。
//
// 读不出来不是错误：大多是还没有人选过。那时提取会去看默认位置，再不行就把页面交回给人——
// 页面上那个按钮是唯一弹选择框的地方。
func savedClientFolder() string {
	data, err := os.ReadFile(clientFilePath())
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("client folder: cannot read it", "path", clientFilePath(), "error", err)
		}
		return ""
	}
	var c clientFolder
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Warn("client folder: cannot parse it", "path", clientFilePath(), "error", err)
		return ""
	}
	return strings.TrimSpace(c.Path)
}

// saveClientFolder records the choice. 写不进去只是下次要再选一遍，不该让提取失败。
func saveClientFolder(folder string) error {
	if strings.TrimSpace(folder) == "" {
		return nil
	}
	data, err := json.MarshalIndent(clientFolder{Path: folder, ChosenAt: time.Now()}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(appDataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(clientFilePath(), append(data, '\n'), 0o644)
}

// defaultClientFolder 在几个常见位置里找一个像客户端的东西，找不到给空串。
//
// 这几条与游戏自己的 setup.mjs 里那份候选表一致（clientCandidates）：先用我们这边知道的，找不到
// 才去问人。两边都只是"猜得准一点"的启发式，真正认不认由 extract.py 说了算。
func defaultClientFolder() string {
	const tail = `Arknights_Data\\StreamingAssets\\AB\\Windows`
	bases := []string{
		`%ProgramFiles%\\Hypergryph Launcher\\games\\Arknights`,
		`%ProgramFiles(x86)%\\Hypergryph Launcher\\games\\Arknights`,
		`%LOCALAPPDATA%\\Hypergryph Launcher\\games\\Arknights`,
		`%USERPROFILE%\\Games\\Hypergryph Launcher\\games\\Arknights`,
		`%ProgramFiles%\\Hypergryph\\Arknights`,
		`%USERPROFILE%\\Arknights`,
	}
	for _, d := range []string{"C", "D", "E", "F", "G", "H"} {
		for _, base := range bases {
			path := os.ExpandEnv(base)
			if path == "" {
				continue
			}
			if base != path {
				if _, err := os.Stat(filepath.Join(path, tail)); err == nil {
					return filepath.Join(path, tail)
				}
			}
			// 也可能装在某个盘的根下：<盘>:\Arknights\...
			alt := filepath.Join(d+":\\", "Arknights", tail)
			if _, err := os.Stat(alt); err == nil {
				return alt
			}
		}
	}
	return ""
}
