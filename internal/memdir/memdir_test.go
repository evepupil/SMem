package memdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"smem/internal/workspace"
)

func newTestStore(t *testing.T) (*Store, workspace.Layout) {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	smemHome := filepath.Join(home, ".smem")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	layout, err := workspace.Resolve(workspace.ResolveOptions{
		HomeDir:  home,
		SmemHome: smemHome,
		CWD:      repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(layout), layout
}

func TestFrontmatterRoundTripAndPlainMarkdown(t *testing.T) {
	fm := Frontmatter{
		Name:        "API: use v2 endpoint",
		Description: "when calling checkout API, always use v2",
		Type:        TypeFeedback,
		Extra:       []ExtraField{{Key: "owner", Value: "platform"}},
	}
	raw := RenderDocument(fm, "Always call `/v2/checkout`.\n\n**Why:** v1 is deprecated.")
	parsedFM, body, hasFM := ParseDocument(raw, "feedback_api.md")
	if !hasFM {
		t.Fatalf("expected hasFM=true")
	}
	if parsedFM.Name != fm.Name || parsedFM.Description != fm.Description || parsedFM.Type != TypeFeedback {
		t.Fatalf("frontmatter mismatch: got %+v, want %+v", parsedFM, fm)
	}
	if len(parsedFM.Extra) != 1 || parsedFM.Extra[0].Key != "owner" || parsedFM.Extra[0].Value != "platform" {
		t.Fatalf("extra fields not preserved: %+v", parsedFM.Extra)
	}
	if !strings.Contains(body, "/v2/checkout") {
		t.Fatalf("unexpected body: %q", body)
	}

	// Hand-written plain Markdown without frontmatter.
	plain := []byte("# Deploy Notes\n\nAlways rebuild Docker container after config edit.\n")
	plainFM, plainBody, hasPlainFM := ParseDocument(plain, "deploy.md")
	if hasPlainFM {
		t.Fatalf("expected hasFM=false for plain markdown")
	}
	if plainFM.Name != "Deploy Notes" || plainFM.Type != TypeUntyped {
		t.Fatalf("unexpected fallback frontmatter: %+v", plainFM)
	}
	if !strings.Contains(plainBody, "Docker container") {
		t.Fatalf("unexpected plain body: %q", plainBody)
	}
}

func TestWriteReadUpdateDeleteAndIndexSync(t *testing.T) {
	store, _ := newTestStore(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	store.Now = func() time.Time { return now }

	// 1. Create a project memory.
	w1, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeProject,
		Name:        "生产发版审批人",
		Description: "涉及生产发版或上线交接单时触发",
		Content:     "本项目生产发版必须由阿宁放行。",
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if w1.Status != "created" || w1.ETag == "" {
		t.Fatalf("unexpected write result: %+v", w1)
	}

	// Verify MEMORY.md was atomically updated.
	idxRes, err := store.Index("project")
	if err != nil {
		t.Fatal(err)
	}
	if idxRes.Project == nil || !strings.Contains(idxRes.Project.IndexContent, w1.File) {
		t.Fatalf("MEMORY.md missing pointer for %s: %+v", w1.File, idxRes.Project)
	}

	// 2. Idempotent duplicate write returns unchanged.
	dup, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeProject,
		Name:        "生产发版审批人",
		Description: "涉及生产发版或上线交接单时触发",
		Content:     "本项目生产发版必须由阿宁放行。",
	})
	if err != nil {
		t.Fatalf("idempotent write failed: %v", err)
	}
	if dup.Status != "unchanged" || dup.File != w1.File {
		t.Fatalf("expected unchanged reuse, got %+v", dup)
	}

	// 3. Same name with different content without file+etag is rejected to prevent duplicates.
	if _, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeProject,
		Name:        "生产发版审批人",
		Description: "发版审批",
		Content:     "改为小周放行。",
	}); err == nil {
		t.Fatalf("expected duplicate-name conflict error when file+etag omitted")
	}

	// 4. Update with wrong ETag fails.
	if _, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeProject,
		Name:        "生产发版审批人",
		Description: "涉及生产发版或上线交接单时触发",
		Content:     "本项目生产发版必须由小周放行。",
		File:        w1.File,
		ETag:        "stale0000000",
	}); err == nil {
		t.Fatalf("expected stale ETag rejection")
	}

	// 5. Update with valid ETag succeeds in place, replacing old value cleanly.
	w2, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeProject,
		Name:        "生产发版审批人",
		Description: "涉及生产发版或上线交接单时触发",
		Content:     "本项目生产发版必须由小周放行。",
		File:        w1.File,
		ETag:        w1.ETag,
	})
	if err != nil {
		t.Fatalf("valid update failed: %v", err)
	}
	if w2.Status != "updated" || w2.ETag == w1.ETag {
		t.Fatalf("unexpected update result: %+v", w2)
	}

	// Simulate 14 days passing to verify memoryAge staleness warning.
	store.Now = func() time.Time { return now.Add(14 * 24 * time.Hour) }
	entries, err := store.Read(ScopeProject, []string{w1.File})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0].Body, "小周") || strings.Contains(entries[0].Body, "阿宁") {
		t.Fatalf("expected clean updated body without old value: %+v", entries)
	}
	if entries[0].AgeDays != 14 || entries[0].StaleWarning == "" {
		t.Fatalf("expected 14-day age & stale warning, got age=%q days=%d warn=%q", entries[0].Age, entries[0].AgeDays, entries[0].StaleWarning)
	}

	// 6. Delete with stale ETag fails; delete with current ETag removes file & index line.
	if err := store.Delete(DeleteRequest{Scope: ScopeProject, File: w1.File, ETag: w1.ETag}); err == nil {
		t.Fatalf("expected delete with old ETag to fail")
	}
	if err := store.Delete(DeleteRequest{Scope: ScopeProject, File: w1.File, ETag: w2.ETag}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	idxAfter, err := store.Index("project")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(idxAfter.Project.IndexContent, w1.File) {
		t.Fatalf("MEMORY.md still references deleted file %s", w1.File)
	}
}

func TestSecretBlockAndPrivateStrip(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeReference,
		Name:        "API Token",
		Description: "test",
		Content:     "Use sk-ant-api03-1234567890abcdefghijklmnop for auth",
	})
	if err == nil {
		t.Fatalf("expected secret detection error")
	}

	w, err := store.Write(WriteRequest{
		Scope:       ScopeGlobal,
		Type:        TypeUser,
		Name:        "沟通偏好",
		Description: "回复风格偏好",
		Content:     "回复尽量简洁直接。<private>临时密钥 sk-ant-api03-1234567890abcdefghijklmnop</private>",
	})
	if err != nil {
		t.Fatalf("expected private block to be stripped cleanly, got err: %v", err)
	}
	if strings.Contains(w.ContentEcho, "sk-ant-") {
		t.Fatalf("private secret leaked into saved content")
	}
}

func TestSelfHealingUnindexedAndCheckFix(t *testing.T) {
	store, layout := newTestStore(t)
	dir := layout.ProjectDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Simulate Claude Code or a human creating a topic file directly without updating MEMORY.md.
	orphan := RenderDocument(Frontmatter{
		Name:        "外部手写笔记",
		Description: "直接放到目录中的主题文件",
		Type:        TypeProject,
	}, "改完配置需要重建容器。")
	if err := os.WriteFile(filepath.Join(dir, "project_orphan.md"), orphan, 0600); err != nil {
		t.Fatal(err)
	}

	rendered, idx, err := store.RenderCombinedIndex("project")
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Project.Unindexed) != 1 || !strings.Contains(rendered, "project_orphan.md") {
		t.Fatalf("expected self-healed unindexed file in rendered index, got %+v\n%s", idx.Project, rendered)
	}

	report, err := store.Check("project", true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Fixed {
		t.Fatalf("expected Check(fix=true) to rebuild MEMORY.md")
	}
	idxAfter, err := store.Index("project")
	if err != nil {
		t.Fatal(err)
	}
	if len(idxAfter.Project.Unindexed) != 0 {
		t.Fatalf("expected 0 unindexed files after fix, got %+v", idxAfter.Project.Unindexed)
	}
}

func TestSearchCodeIdentifiersAndCJK(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeProject,
		Name:        "容器重建规范",
		Description: "修改发布配置后需重建容器",
		Content:     "修改 config.yaml 后，调用 rebuildOrderWorker 不会热加载，必须 docker compose up --build。",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Write(WriteRequest{
		Scope:       ScopeGlobal,
		Type:        TypeFeedback,
		Name:        "简洁回复",
		Description: "代码修改后不需要冗长总结",
		Content:     "改完代码不要在结尾复述改动清单。",
	})
	if err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search(SearchOptions{Query: "OrderWorker 容器"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Name != "容器重建规范" {
		t.Fatalf("expected 容器重建规范 as top hit, got %+v", hits)
	}
}

func TestNonSilentTruncationOver200Lines(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Project Memory Index\n\n")
	for i := 1; i <= 215; i++ {
		b.WriteString(fmt.Sprintf("- [Topic %d](t%d.md) — trigger %d\n", i, i, i))
	}
	bounded, truncated, omitted := truncateIndexNonSilent(b.String(), MaxIndexLines, MaxIndexBytes)
	if !truncated || omitted <= 0 {
		t.Fatalf("expected non-silent truncation, got truncated=%v omitted=%d", truncated, omitted)
	}
	if !strings.Contains(bounded, "SMem 索引提示") {
		t.Fatalf("expected explicit overflow notice in truncated index")
	}
}

func TestConcurrentWritesSafe(t *testing.T) {
	store, _ := newTestStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := store.Write(WriteRequest{
				Scope:       ScopeProject,
				Type:        TypeProject,
				Name:        fmt.Sprintf("并发条目 %d", idx),
				Description: fmt.Sprintf("并发测试说明 %d", idx),
				Content:     fmt.Sprintf("并发写入内容 %d", idx),
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write error: %v", err)
	}

	idxRes, err := store.Index("project")
	if err != nil {
		t.Fatal(err)
	}
	if idxRes.Project.TopicCount != 12 || len(idxRes.Project.Unindexed) != 0 {
		t.Fatalf("expected all 12 concurrent writes indexed cleanly, got count=%d unindexed=%d",
			idxRes.Project.TopicCount, len(idxRes.Project.Unindexed))
	}
}

func TestHubWikiLinksAndRebuildPreservesSubMemories(t *testing.T) {
	store, layout := newTestStore(t)
	dir := layout.ProjectDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	mustWrite := func(file string, fm Frontmatter, body string) {
		t.Helper()
		raw := RenderDocument(fm, body)
		if err := os.WriteFile(filepath.Join(dir, file), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("dev-hub.md", Frontmatter{
		Name:        "3333 故障汇总",
		Description: "本地 dev 服务器故障入口",
		Type:        TypeReference,
	}, "汇总入口：\n- [[sub-crash|无声退出]]\n- [缓存死循环](sub-cache.md#clean)\n")

	mustWrite("sub-crash.md", Frontmatter{
		Name:        "无声硬崩",
		Description: "0xC0000005 崩溃处理",
		Type:        TypeProject,
	}, "固定能效核即可规避。")

	mustWrite("sub-cache.md", Frontmatter{
		Name:        "缓存死循环",
		Description: ".next 缓存损坏",
		Type:        TypeProject,
	}, "清理 .next 重新启动。")

	mustWrite("true-orphan.md", Frontmatter{
		Name:        "未挂接笔记",
		Description: "没有任何链接指向",
		Type:        TypeProject,
	}, "这是一条孤儿笔记。")

	indexRaw := "# Project Memory Index\n\n- [3333 故障汇总](dev-hub.md) — 本地 dev 服务器故障入口\n"
	if err := os.WriteFile(filepath.Join(dir, IndexFileName), []byte(indexRaw), 0600); err != nil {
		t.Fatal(err)
	}

	rendered, idxRes, err := store.RenderCombinedIndex("project")
	if err != nil {
		t.Fatal(err)
	}
	if idxRes.Project.SubIndexedCount != 2 {
		t.Fatalf("expected 2 sub-indexed Hub files, got %d", idxRes.Project.SubIndexedCount)
	}
	if len(idxRes.Project.Unindexed) != 1 || idxRes.Project.Unindexed[0].File != "true-orphan.md" {
		t.Fatalf("expected only true-orphan.md in Unindexed, got %+v", idxRes.Project.Unindexed)
	}
	if strings.Contains(rendered, "sub-crash.md") || strings.Contains(rendered, "sub-cache.md") {
		t.Fatalf("sub-indexed Hub children must not bloat rendered root index:\n%s", rendered)
	}

	// Reading Hub file exposes resolved child filenames.
	entries, err := store.Read(ScopeProject, []string{"dev-hub.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries[0].LinkedFiles) != 2 {
		t.Fatalf("expected 2 linked child files on hub Read, got %+v", entries[0].LinkedFiles)
	}

	// Check(fix=true) indexes true-orphan.md into MEMORY.md while keeping sub-crash & sub-cache archived under dev-hub.md!
	report, err := store.Check("project", true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Fixed {
		t.Fatalf("expected Check(fix=true) to repair true-orphan.md")
	}
	rebuiltBytes, err := os.ReadFile(filepath.Join(dir, IndexFileName))
	if err != nil {
		t.Fatal(err)
	}
	rebuiltStr := string(rebuiltBytes)
	if !strings.Contains(rebuiltStr, "dev-hub.md") || !strings.Contains(rebuiltStr, "true-orphan.md") {
		t.Fatalf("rebuilt MEMORY.md missing root entries:\n%s", rebuiltStr)
	}
	if strings.Contains(rebuiltStr, "sub-crash.md") || strings.Contains(rebuiltStr, "sub-cache.md") {
		t.Fatalf("rebuilt MEMORY.md flattened Hub children instead of preserving hierarchy:\n%s", rebuiltStr)
	}
}

func TestPriorityTruncationPreservesBottomFeedbackAndPinned(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Project Memory Index\n\n")
	typeByFile := make(map[string]MemoryType)
	for i := 1; i <= 210; i++ {
		f := fmt.Sprintf("proj_%d.md", i)
		typeByFile[f] = TypeProject
		b.WriteString(fmt.Sprintf("- [普通条目 %d](%s) — 一般说明 %d\n", i, f, i))
	}
	typeByFile["fb_tail.md"] = TypeFeedback
	b.WriteString("- [尾部纠错规矩](fb_tail.md) — 提交前跑门禁\n")
	typeByFile["pinned_tail.md"] = TypeProject
	b.WriteString("- [只开一台共用服务器（必须记）](pinned_tail.md) — 固定 3333 端口\n")

	bounded, truncated, omitted := truncateIndexWithPriority(b.String(), typeByFile, MaxIndexLines, MaxIndexBytes)
	if !truncated || omitted <= 0 {
		t.Fatalf("expected truncation, got truncated=%v omitted=%d", truncated, omitted)
	}
	if !strings.Contains(bounded, "fb_tail.md") || !strings.Contains(bounded, "pinned_tail.md") {
		t.Fatalf("expected bottom feedback and (必须记) entries preserved by priority truncation:\n%s", bounded)
	}
}

func TestBM25FRareTermAndStopwordFiltering(t *testing.T) {
	store, _ := newTestStore(t)

	// Populate several noisy docs sharing common token "3333", including a long verbose log dump.
	noisyBody := strings.Repeat("3333 服务器日常启动日志正常，端口 3333 已监听。\n", 30)
	for i := 1; i <= 4; i++ {
		if _, err := store.Write(WriteRequest{
			Scope:       ScopeProject,
			Type:        TypeProject,
			Name:        fmt.Sprintf("3333 常规记录 %d", i),
			Description: "3333 日常运行说明",
			Content:     noisyBody,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// One concise note containing the rare diagnostic error code 0xC0000005.
	if _, err := store.Write(WriteRequest{
		Scope:       ScopeProject,
		Type:        TypeFeedback,
		Name:        "3333 硬崩排查",
		Description: "退出码 0xC0000005 时触发",
		Content:     "V8 在性能核触发 0xC0000005 无声崩溃，绑定能效核解决。",
	}); err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search(SearchOptions{Query: "请问帮我看看怎么处理 3333 的 0xC0000005 问题"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Name != "3333 硬崩排查" {
		t.Fatalf("expected concise rare-term match 3333 硬崩排查 ranked #1, got %+v", hits)
	}
}
