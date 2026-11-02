# 模型映射说明（Codex 桌面端）

桌面端的模型下拉框是固定的 8 个名字，名字背后对应 **Qoder / WorkBuddy 的实际模型**（两边是同一套模型，自动轮询、互为冗余）。

| 桌面端选择 | 实际模型 | 成本 |
|---|---|---|
| **6.1 Sol** | `deepseek-v4.1-flash` | **x0.00** |
| **6 Astra** | `balanced-model` | x0.59 |
| **6 Sol** | `glm-5.3` | x0.69 |
| **6 Luna** | `hy3`（腾讯混元） | **x0.00** |
| **5.6 Sol** | `default-model`（Auto） | x1.00 |
| **5.6 Terra** | `hy4-preview` | x0.29 |
| **5.6 Luna** | `fast-model` | x0.34 |
| **5.5** | `glm-5.3-flash` | x0.08 |

## 免费 / 便宜的推荐

- **零成本**：`6 Luna`（hy3）、`6.1 Sol`（deepseek-v4.1-flash，免费额度用完会自动降级到付费档）
- **便宜**：`5.5`（glm-5.3-flash, x0.08）、`5.6 Luna`（fast-model, x0.34）
- **质量优先**：`6 Sol`（glm-5.3, x0.69）、`5.6 Sol`（default-model, x1.00）

## 6.1 Sol 的自动降级链

`6.1 Sol` 不是单一模型，而是按顺序兜底：

```
① deepseek-v4.1-flash   免费        ← 默认
② hy4-preview           x0.29      ← 免费额度用完
③ glm-5.3               x0.69
④ 真 gpt-6.1-sol        (asxs)     ← 兜底
```

任一级失败（额度耗尽 / 超时）会自动切下一级，**用户无需改设置**。

## 其他模型怎么用

桌面端下拉框之外的模型（如 `kimi-k3`、`glm-5.2`、`deepseek-v4.1-flash-sg` 等）可通过 API 或修改 `~/.codex/config.toml` 直接指定：

```toml
model = "hy3"        # 或 kimi-k3 / glm-5.2 / deepseek-v4.1-flash ...
```

当前可用的原生模型名：

- **免费**：`hy3`、`deepseek-v4.1-flash`
- **轻量**：`glm-5.3-flash`、`fast-model`、`deepseek-v4.1-flash-sg`
- **标准**：`balanced-model`、`glm-5.3`、`hy4-preview`、`default-model`
- **重负载**：`primary-model`（x3.31，可能受套餐限制）
