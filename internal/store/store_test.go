// 删除与还原的回归测试。
// 全部在临时目录里跑，不碰真实数据；进程检查被替换成「永远没在运行」。
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dsh-cleaner/internal/config"

	"github.com/klauspost/compress/zstd"
)

func TestMain(m *testing.M) {
	checkProcess = func(string) bool { return false } // 测试环境里忽略真实客户端进程
	os.Exit(m.Run())
}

type fixture struct {
	paths config.Paths
}

// newFixture 造一个最小可用的 DSH 数据目录
func newFixture(t *testing.T) *fixture {
	t.Helper()
	paths := config.Paths{Home: t.TempDir()}
	for _, d := range []string{paths.Sessions(), paths.Storages(), paths.Trash()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &fixture{paths: paths}
}

// session 写一个会话目录。刻意分成两个 zstd frame，模拟客户端追加写入。
func (f *fixture) session(t *testing.T, wsDir, id, title, cwd string) string {
	t.Helper()
	dir := filepath.Join(f.paths.Sessions(), wsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	var blob []byte
	for _, ev := range []map[string]any{
		{"type": "session", "time": 1000, "data": map[string]any{"cwd": cwd, "createdAt": 1000}},
		{"type": "session/title", "time": 2000, "data": map[string]any{"title": title}},
	} {
		line, _ := json.Marshal(ev)
		blob = append(blob, enc.EncodeAll(append(line, '\n'), nil)...)
	}
	if err := os.WriteFile(filepath.Join(dir, config.SessionFile), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (f *fixture) index(t *testing.T, raw string) {
	t.Helper()
	if err := os.WriteFile(f.paths.Index(), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) readIndex(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(f.paths.Index())
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

const testIndex = `{
  "global": {
    "workspaceIds": ["ws1"],
    "archivedSessionIds": ["s2"],
    "pinnedSessionIds": [],
    "defaultWorkspaceId": "ws1"
  },
  "tables": {
    "workspaces": {
      "ws1": {"title": "项目A", "path": "E:\\projA", "sessionIds": ["s1", "s2"]}
    }
  }
}`

func standardFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.session(t, "--E-projA--", "s1", "正常会话", `E:\projA`)
	f.session(t, "--E-projA--", "s2", "已归档的会话", `E:\projA`)
	f.index(t, testIndex)
	return f
}

// 删除归档：目录进隔离区、索引里归档与工作区归属一起清掉、清单标题正确
func TestRemoveArchivedMovesToTrashAndCleansIndex(t *testing.T) {
	f := standardFixture(t)

	res, err := RemoveArchived(f.paths, false)
	if err != nil {
		t.Fatalf("删除归档失败: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("应删除 1 个，实际 %d（失败项 %v）", res.Removed, res.Failed)
	}
	if _, err := os.Stat(filepath.Join(f.paths.Sessions(), "--E-projA--", "s2")); !os.IsNotExist(err) {
		t.Error("会话目录应已从 sessions 移走")
	}

	items := ListTrash(f.paths)
	if len(items) != 1 || len(items[0].Items) != 1 {
		t.Fatalf("隔离区应有 1 个批次 1 条记录，实际 %+v", items)
	}
	if got := items[0].Items[0].Title; got != "已归档的会话" {
		t.Errorf("清单标题应为会话标题，实际 %q", got)
	}
	if !items[0].Items[0].Archived {
		t.Error("清单应记住归档状态，还原时才能恢复")
	}

	d := f.readIndex(t)
	if got := d["global"].(map[string]any)["archivedSessionIds"].([]any); len(got) != 0 {
		t.Errorf("归档列表应清空，实际 %v", got)
	}
	ws := d["tables"].(map[string]any)["workspaces"].(map[string]any)["ws1"].(map[string]any)
	if got := ws["sessionIds"].([]any); len(got) != 1 || got[0] != "s1" {
		t.Errorf("工作区应只剩 s1，实际 %v", got)
	}
}

// 还原：文件回到原位，归档状态一并恢复
func TestRestoreBringsBackFileAndArchivedFlag(t *testing.T) {
	f := standardFixture(t)
	if _, err := RemoveArchived(f.paths, false); err != nil {
		t.Fatal(err)
	}
	entry := ListTrash(f.paths)[0].Dir

	n, err := Restore(f.paths, entry, nil)
	if err != nil {
		t.Fatalf("还原失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应还原 1 个，实际 %d", n)
	}
	if _, err := os.Stat(filepath.Join(f.paths.Sessions(), "--E-projA--", "s2", config.SessionFile)); err != nil {
		t.Errorf("会话文件应已回到原位: %v", err)
	}

	d := f.readIndex(t)
	archived := d["global"].(map[string]any)["archivedSessionIds"].([]any)
	found := false
	for _, id := range archived {
		if id == "s2" {
			found = true
		}
	}
	if !found {
		t.Errorf("还原后归档状态应恢复，实际 %v", archived)
	}
	ws := d["tables"].(map[string]any)["workspaces"].(map[string]any)["ws1"].(map[string]any)
	if got := ws["sessionIds"].([]any); len(got) != 2 {
		t.Errorf("工作区应恢复为 2 个会话，实际 %v", got)
	}
}

// 安全：清空隔离区只能动自己创建的批次，不能变成任意目录删除器
func TestPurgeRejectsPathsOutsideTrash(t *testing.T) {
	f := newFixture(t)

	victim := filepath.Join(f.paths.Home, "重要资料")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		victim,          // 不在 trash 下
		f.paths.Trash(), // trash 本身
		filepath.Join(f.paths.Trash(), "..", "重要资料"), // 路径穿越
		filepath.Join(f.paths.Trash(), "random-dir"), // 前缀不对
	} {
		if err := Purge(f.paths, bad); err == nil {
			t.Errorf("应拒绝非法路径: %s", bad)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatalf("非法请求不该删掉任何东西: %v", err)
		}
	}

	// 合法批次仍可清空
	legal := filepath.Join(f.paths.Trash(), config.TrashPrefix+"20260101-000000")
	if err := os.MkdirAll(legal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Purge(f.paths, legal); err != nil {
		t.Fatalf("合法批次应能清空: %v", err)
	}
	if _, err := os.Stat(legal); !os.IsNotExist(err) {
		t.Error("合法批次应已被删除")
	}
}

// 传入不存在的会话 id：不能把索引里的记录摘掉（文件还在、界面却消失最危险）
func TestRemoveSessionsIgnoresUnknownIDs(t *testing.T) {
	f := standardFixture(t)

	res, err := RemoveSessions(f.paths, []string{"不存在的会话"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 {
		t.Fatalf("不该删掉任何东西，实际 %d", res.Removed)
	}
	d := f.readIndex(t)
	ws := d["tables"].(map[string]any)["workspaces"].(map[string]any)["ws1"].(map[string]any)
	if got := ws["sessionIds"].([]any); len(got) != 2 {
		t.Errorf("索引不该被改动，实际 %v", got)
	}
}

// 删除工作区：会话清掉、工作区从索引消失、清单里是会话自己的标题
func TestRemoveWorkspace(t *testing.T) {
	f := standardFixture(t)

	res, err := RemoveWorkspace(f.paths, "ws1", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 {
		t.Fatalf("应删除 2 个会话，实际 %d（失败 %v）", res.Removed, res.Failed)
	}

	items := ListTrash(f.paths)
	titles := map[string]bool{}
	for _, it := range items[0].Items {
		titles[it.Title] = true
	}
	if !titles["正常会话"] || !titles["已归档的会话"] {
		t.Errorf("清单里应是会话标题，实际 %v", titles)
	}
	if titles["项目A"] {
		t.Error("不该把工作区名当初会话标题")
	}

	d := f.readIndex(t)
	if _, ok := d["tables"].(map[string]any)["workspaces"].(map[string]any)["ws1"]; ok {
		t.Error("工作区应已从索引删除")
	}
	g := d["global"].(map[string]any)
	if got := g["workspaceIds"].([]any); len(got) != 0 {
		t.Errorf("workspaceIds 应清空，实际 %v", got)
	}
	if _, ok := g["defaultWorkspaceId"]; ok {
		t.Error("默认工作区指向已删工作区时应移除该字段")
	}
}

// 扫描：标题、工作区路径、归档标记、孤儿判定都要准
func TestScanMarksStates(t *testing.T) {
	f := standardFixture(t)
	// 一个索引外的孤儿会话
	f.session(t, "--E-orphan--", "s3", "孤儿会话", `E:\orphan`)

	snap, err := Scan(f.paths)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Session{}
	for _, s := range snap.Sessions {
		byID[s.ID] = s
	}
	if len(snap.Sessions) != 3 {
		t.Fatalf("应扫出 3 个会话，实际 %d", len(snap.Sessions))
	}
	if !byID["s2"].Archived {
		t.Error("s2 应标记为已归档")
	}
	if byID["s3"].InIndex {
		t.Error("s3 不在索引里，应判为未分组")
	}
	if byID["s3"].Title != "孤儿会话" {
		t.Errorf("孤儿会话标题应能读出，实际 %q", byID["s3"].Title)
	}
	if !strings.Contains(byID["s3"].WorkspacePath, "orphan") {
		t.Errorf("孤儿会话应能从目录名还原路径，实际 %q", byID["s3"].WorkspacePath)
	}
}

// 同一秒内连续删两次，批次目录不能重名（重名会导致后一次清单覆盖前一次）
func TestTrashDirNameCollision(t *testing.T) {
	root := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		dir, err := newTrashDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if seen[dir] {
			t.Fatalf("批次目录重名: %s", dir)
		}
		seen[dir] = true
	}
}

// 早期版本写隔离区时不写 manifest，只有目录结构。
// 这种批次必须能正常列出（不能是 0 个会话），也必须能还原。
func TestLegacyBatchWithoutManifest(t *testing.T) {
	f := standardFixture(t)
	legacy := filepath.Join(f.paths.Trash(), config.TrashPrefix+"legacy")
	sessionDir := filepath.Join(legacy, "--E-projA--", "session-old1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, config.SessionFile), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := ListTrash(f.paths)
	if len(batch) != 1 {
		t.Fatalf("应有 1 个批次，实际 %d", len(batch))
	}
	if got := len(batch[0].Items); got != 1 {
		t.Fatalf("无清单批次也应按目录扫出 1 个会话，实际 %d", got)
	}
	if got := batch[0].Items[0].FromRel; got != filepath.Join("--E-projA--", "session-old1") {
		t.Errorf("重建的原始位置不对: %q", got)
	}

	n, err := Restore(f.paths, legacy, nil)
	if err != nil {
		t.Fatalf("旧批次还原失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应还原 1 个，实际 %d", n)
	}
	if _, err := os.Stat(filepath.Join(f.paths.Sessions(), "--E-projA--", "session-old1", config.SessionFile)); err != nil {
		t.Errorf("会话文件应已回到原位: %v", err)
	}
}

// 索引写回必须保留不认识的字段，客户端升级新增字段不能被本工具吃掉
func TestIndexKeepsUnknownFields(t *testing.T) {
	f := newFixture(t)
	f.index(t, `{
      "global": {"workspaceIds": [], "archivedSessionIds": [], "futureFlag": {"nested": 1}},
      "tables": {"workspaces": {}, "sessions": {"x": 1}},
      "schemaVersion": 7
    }`)
	ix, err := LoadIndex(f.paths.Index())
	if err != nil {
		t.Fatal(err)
	}
	ix.DetachSessions([]string{"nothing"})
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}

	d := f.readIndex(t)
	if d["schemaVersion"] != float64(7) {
		t.Errorf("顶层未知字段丢了: %v", d["schemaVersion"])
	}
	if _, ok := d["tables"].(map[string]any)["sessions"]; !ok {
		t.Error("tables 下的未知表丢了")
	}
	if _, ok := d["global"].(map[string]any)["futureFlag"]; !ok {
		t.Error("global 下的未知字段丢了")
	}
}
