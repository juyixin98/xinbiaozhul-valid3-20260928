// Package logging 提供结构化事件日志。每条记录至少包含：
//
//	ts      ISO8601 时间戳
//	level   info | warn | error
//	run     运行标识（进程启动时生成，测试可显式注入）
//	stage   连接生命周期阶段（accept/read-request/framing/body/...）
//	msg     事件描述
//
// 协议错误额外带 kind / phase / offset / rel，保证“按字节偏移定位”。
// 四类失败在 level 与 kind 上可区分：输入非法(warn+invalid_request)、
// 状态冲突(warn+*)、资源超限(warn+*_too_large)、运行失败(error)。
// 未定义/未收敛结果不允许以 info 成功记录。
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// Logger 是并发安全的结构化记录器。
type Logger struct {
	mu    sync.Mutex
	w     io.Writer
	runID string
	json  bool
}

// New 构造记录器。runID 为空时用进程级默认值。
func New(w io.Writer, runID string, asJSON bool) *Logger {
	if runID == "" {
		runID = DefaultRunID
	}
	return &Logger{w: w, runID: runID, json: asJSON}
}

// DefaultRunID 是未显式指定时的运行标识（可在 main 中覆盖）。
var DefaultRunID = "run"

// Fields 是事件附加字段。
type Fields map[string]any

func (l *Logger) log(level, stage, msg string, f Fields) {
	rec := map[string]any{
		"ts":    time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
		"level": level,
		"run":   l.runID,
		"stage": stage,
		"msg":   msg,
	}
	for k, v := range f {
		rec[k] = v
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.json {
		_ = json.NewEncoder(l.w).Encode(rec)
		return
	}
	fmt.Fprintf(l.w, "%s %-5s run=%s stage=%s %s", rec["ts"], level, l.runID, stage, msg)
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(l.w, " %s=%v", k, formatVal(f[k]))
	}
	fmt.Fprintln(l.w)
}

func formatVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

// Info 记录正常路径与判定依据。
func (l *Logger) Info(stage, msg string, f Fields) { l.log("info", stage, msg, f) }

// Warn 记录客户端可归因问题（非法输入、定帧冲突、资源超限）。
func (l *Logger) Warn(stage, msg string, f Fields) { l.log("warn", stage, msg, f) }

// Error 记录服务端运行失败（IO 错误、存储错误、panic）。
func (l *Logger) Error(stage, msg string, f Fields) { l.log("error", stage, msg, f) }

// Discard 返回丢弃全部输出的记录器（测试中与显式断言记录器二选一）。
func Discard() *Logger { return New(io.Discard, "test", false) }

// Std 返回写到 stdout 的记录器。
func Std(runID string, asJSON bool) *Logger { return New(os.Stdout, runID, asJSON) }
