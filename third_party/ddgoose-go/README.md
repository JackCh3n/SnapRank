# ddgoose-go CI 内嵌快照

本目录是压缩引擎依赖 [DD鹅 ddgoose-go](https://github.com/JackCh3n/ddGoose-go) 的
最小快照，仅用于 CI（GitHub Actions / CNB）在无法克隆上游私有仓库时兜底构建。

## 内容

| 文件 | 说明 |
|---|---|
| `go.mod` | 最小 module 声明（`ui` 包仅依赖标准库） |
| `ui/` | 压缩器源码（compressor.go / logger.go / utils.go），与上游 `ddgoose-go/ui` 一致 |
| `lib/cjpeg-mod.exe` | MozJPEG 压缩引擎。SnapRank 预处理统一转 JPEG 后只调用该工具（其余 pngquant/oxipng 等上游工具用不到） |

## 更新方式

上游 `ddgoose-go/ui` 变更后重新拷贝对应源文件与引擎即可：

```bash
cp ../ddGoose-go/ui/{compressor,logger,utils}.go third_party/ddgoose-go/ui/
cp ../ddGoose-go/lib/cjpeg-mod.exe third_party/ddgoose-go/lib/
```

本地开发不受此快照影响：go.mod 的 `replace ddgoose-go => ../ddGoose-go` 始终指向本地真实项目。
