# 运行配置文件挂载

本目录以只读方式挂载到 API 容器的 `/app/config`，适合保存不含凭据的版本化配置。

## 模型价格

从 [model-pricing.example.json](../../backend/config/model-pricing.example.json)创建自己的价格文件，例如 `model-pricing.json`，在部署环境设置：

~~~dotenv
MODEL_PRICING_FILE=/app/config/model-pricing.json
~~~

修改后重新创建 API，使进程读取新版本。价格按返回模型名称匹配，旧调用保留原快照；缺少价格时费用未知。

此目录不存 API Key、账号或认证文件。完整格式与统计边界见[模型用量](../../docs/MODEL_USAGE.md)。
