//go:build windows

package main

// 游戏源码的「哪一版」——用 commit hash，不用 package.json 里的版本号。
//
// 版本号是给 npm 看的，它只在维护者想起来的时候才动；而"上游有没有新东西"这个问题，真正在动的
// 是 master 上那个 commit。所以本机记的是取源码那一刻的 hash，上游问的也是 hash。
//
// 本机这一份从哪来：源码是 tar.gz 解包出来的，游戏目录里**没有 .git**，没有现成的 hash 可读。
// 所以取源码的时候顺手把它记在一个小文件里（记在数据目录，不记在游戏目录——那是上游的地盘，
// 覆盖式解包会把它清掉）。
//
// 上游那一份从哪来：GitHub 的 API；API 不行就退到 master 的 atom feed，它同样带着每个 commit 的
// hash，而且不占 API 的限流。

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// upstreamCommit 问的是归档所在那个分支上最新的一个 commit。
	upstreamCommit = "https://api.github.com/repos/sganggs/Stronghold-Protocol/commits?per_page=1"

	// upstreamFeed 是同一个答案的另一种形式，留给 API 说不行的时候：403（限流），或者一个只放
	// 部分主机出去的代理。
	upstreamFeed = "https://github.com/sganggs/Stronghold-Protocol/commits/master.atom"

	// upstreamWait 是这一问的上限。它是一次给标题栏看的顺路检查：网络慢不该拖住任何东西，而一个
	// 来迟了的答案一文不值。
	upstreamWait = 10 * time.Second
)

// revisionPath 是本程序记下「解包出来的是哪个 commit」的地方：
// %LOCALAPPDATA%\stronghold-protocol-launcher\game-source.json。
//
// 放在数据目录而不是游戏目录里：游戏目录里的一切都是上游的东西，覆盖式解包会把它清掉；这个文件
// 是本程序自己的账。
func revisionPath() string { return filepath.Join(appDataDir(), "game-source.json") }

// sourceRevision 是那个文件里装的东西。
type sourceRevision struct {
	// Hash 是完整的 commit hash；标题栏上显示的是它的前几位。
	Hash string `json:"hash"`
	// FetchedAt 只为读日志的人留着：出了问题时，"这是什么时候取的"往往比 hash 本身更有用。
	FetchedAt time.Time `json:"fetchedAt"`
}

// localRevision 读出记下的那个 hash；那里没有能读的东西时给空串。
//
// 读不出来不是错误：第一次启动、或者用过老版本的程序，都还没有这个文件。那时标题栏不写 hash。
func localRevision() string {
	data, err := os.ReadFile(revisionPath())
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("game source revision: cannot read it", "path", revisionPath(), "error", err)
		}
		return ""
	}
	var rev sourceRevision
	if err := json.Unmarshal(data, &rev); err != nil {
		slog.Warn("game source revision: cannot parse it", "path", revisionPath(), "error", err)
		return ""
	}
	return strings.TrimSpace(rev.Hash)
}

// saveRevision 记下 game\ 里那份源码来自哪个 commit。
//
// 写不进去只是少一处显示，不该让取源码这件事失败——所以返回值给调用方，由它决定怎么记一笔。
func saveRevision(hash string) error {
	if hash == "" {
		return fmt.Errorf("没有 hash 可记")
	}
	data, err := json.MarshalIndent(sourceRevision{Hash: hash, FetchedAt: time.Now()}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(appDataDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(revisionPath(), append(data, '\n'), 0o644)
}

// shortHash is a commit hash the way a title bar can hold it：七位，与 GitHub 上显示的那个缩写
// 一致——同一个 commit，在哪里看都是同一串字符，不必再换算一次。
func shortHash(hash string) string {
	const n = 7
	if len(hash) <= n {
		return hash
	}
	return hash[:n]
}

// sameRevision 说的是两个 hash 是不是同一个 commit：大小写不算差别，hex 的大小写只是写法问题。
func sameRevision(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b)
}

// atomToken 从 master.atom 的一条 entry 里取出 commit hash。
//
// 那 feed 里每条 entry 都带着 <id>tag:github.com,2008:Grit::Commit/<hash></id>。取第一个就够：它
// 是最新那条。这只是一个后备，所以模式写得直白一点，读的人一眼能看出它在找什么。
var atomToken = regexp.MustCompile(`Grit::Commit/([0-9a-fA-F]{40})`)

// upstreamRevision 问 GitHub：master 此刻在哪一个 commit 上。
func upstreamRevision() (string, error) {
	client := &http.Client{Timeout: upstreamWait}

	hash, err := upstreamFromAPI(client)
	if err == nil {
		return hash, nil
	}
	slog.Info("upstream revision: the API did not answer, trying the feed", "error", err)

	hash, feedErr := upstreamFromFeed(client)
	if feedErr != nil {
		return "", fmt.Errorf("api: %w; feed: %v", err, feedErr)
	}
	return hash, nil
}

// upstreamFromAPI 从那个 REST 端点里读出最新的 commit。
func upstreamFromAPI(client *http.Client) (string, error) {
	body, err := getBody(client, upstreamCommit)
	if err != nil {
		return "", err
	}
	var commits []struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &commits); err != nil {
		return "", err
	}
	if len(commits) == 0 || commits[0].SHA == "" {
		return "", fmt.Errorf("%s 里没有 commit", upstreamCommit)
	}
	return commits[0].SHA, nil
}

// upstreamFromFeed 改从那个分支的 atom feed 里读。
func upstreamFromFeed(client *http.Client) (string, error) {
	body, err := getBody(client, upstreamFeed)
	if err != nil {
		return "", err
	}
	match := atomToken.FindSubmatch(body)
	if match == nil {
		return "", fmt.Errorf("%s 里没有 commit hash", upstreamFeed)
	}
	return string(match[1]), nil
}

// getBody 取上面那两个之一，把正文交回来，而不是不限量地交回来。
func getBody(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
