# 适配器模板

复制本目录作为新适配器的起点：

```
qka adapter init my-adapter
cd my-adapter
qka adapter validate .
```

- `manifest.json`：声明 id/版本/入口、能力 needs、动作与效应、兼容范围。
- `index.mjs`：实现 initialize 与各动作；通过 qka 注入的 http.fetch / log.write 能力工作，不直接持有凭据。
- 校验全程离线：`qka adapter validate <dir>` 不联网、不读凭据。
