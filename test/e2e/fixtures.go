// Package e2e 用真实 TCP 回环连接验证整条服务栈（协议解析 → 连接状态机 →
// 业务处理 → SQLite）。夹具逐字节/按方案切分喂入，断言：
//
//   - 接受/拒绝/截断三态与 manifest 一致；
//   - 流水线请求各得一条响应，顺序不乱；
//   - 坏请求后连接被关闭，残余字节绝不被当成下一请求；
//   - 413/431/400/501/505 状态码可断言；
//   - 日志包含 run/conn/req/阶段/字节偏移，可定位。
//
// 夹具从仓库规范目录 test/fixtures 按相对路径读取（embed 不能跨目录），
// 并对照 manifest 中的 sha256 校验，防止测试目录与签入夹具漂移。
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// Fixture 是 manifest.json 的内存模型（与 cmd/genfixtures 对应）。
type Fixture struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Desc     string `json:"desc"`
	Group    string `json:"group"`
	Expected string `json:"expected"`
	Status   int    `json:"status"`
	Kind     string `json:"kind"`
	Phase    string `json:"phase"`
	Offset   *int64 `json:"offset"`
	Accepts  int    `json:"accepts"`
	Legal    bool   `json:"legal"`
	Note     string `json:"note"`
	SHA256   string `json:"sha256"`
}

// fixturesDir 解析规范夹具目录（test/fixtures），与测试源码位置绑定。
func fixturesDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "fixtures")
}

// LoadFixtures 读取 manifest 与全部 .http 字节，并做 sha256 完整性校验。
func LoadFixtures() ([]Fixture, map[string][]byte, error) {
	dir := fixturesDir()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	var list []Fixture
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil, err
	}
	data := map[string][]byte{}
	for _, f := range list {
		b, err := os.ReadFile(filepath.Join(dir, f.File))
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
			return nil, nil, &fixtureDriftError{ID: f.ID, Want: f.SHA256, Got: got}
		}
		data[f.ID] = b
	}
	return list, data, nil
}

type fixtureDriftError struct {
	ID        string
	Want, Got string
}

func (e *fixtureDriftError) Error() string {
	return "fixture drift for " + e.ID + ": run 'go run ./cmd/genfixtures' (sha256 want=" +
		e.Want + " got=" + e.Got + ")"
}
