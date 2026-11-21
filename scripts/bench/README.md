# 真实客户端交接实验

这是自愿运行的联网实验，会使用本机已有模型通道的额度；普通安装和 `go test` 不调用模型。运行环境：Windows、Python 3.11+、Go、Git、Claude Code 和 Codex CLI。可选另装 Pi 验证通用接入。只用虚构的工作区、用户配置和项目约定，不复制账号文件、会话记录、用户说明或真实记忆；仅复用已有模型路由。临时目录包含合成资料，请选择只有当前用户可访问、没有上层助手说明且不在实际项目里的目录。

在要测试的代码版本下编译两份程序：真正的命令叫 `payload.exe`，同目录的 `mem.exe` 只负责记录输入、退出码和真实返回，再原样交给助手。子进程通过 `PATH` 找到它，人不用在题面里提示使用它。以 Windows PowerShell 为例：

```powershell
go build -trimpath -o bin/bench/current/payload.exe ./cmd/mem
go build -trimpath -o bin/bench/current/mem.exe ./scripts/bench/testdata/recorder
$env:SMEM_BENCH_SCRATCH = 'C:\code\.smem-blind-fixtures'
python -B -X utf8 scripts/bench/handoff.py --bin bin/bench/current --cases .fleet/bench/cases.json --out .fleet/bench/run-1.json --model custom/gpt-6-luna --jobs 2
```

每组 10 个独立案例，两个方向各 5 个。首轮生成案例文件；比较其他版本时沿用**同一个案例文件、脚本、模型、沙箱、并发上限和题面**，输出另取新名。看真实写入、读出正文和最终“放行人”三列；必须三项都对且两个客户端正常完成，才算通过。写入方与接手方用两个全新会话、临时用户和仓库，只在两者间复制共享 Markdown，隐藏题目答案的文件不放进任一工作区。没有生成记忆也要跑接手方，不筛掉失败样本。原生对照请用 `checks.py native`；纠正、忘记、翻看、换项目用 `checks.py lifecycle`；无专用适配用 `generic.py`，参数同上，三者都不需要 `--jobs`。原生对照是立即交接，不能据此评价休眠后才生成的后台记忆。

Codex 限制在工作区写入，只给 `mem` 与 `mem.exe` 增加临时执行许可。正式计分的环境关闭与题目无关的插件；Claude 只启用必要的本地工具。输出报告不含请求头、账号配置与聊天原文。客户端允许小模型调用不代表它背后的模型是厂商自家的模型，查看报告中的模型标识。

开发验证：`go test ./scripts/bench/testdata/recorder`、`python -m mypy --strict scripts/bench`。Python 格式使用 `ruff format scripts/bench`。工具若未安装，只需在单独的测试环境安装；smem 用户不需要 Python。
