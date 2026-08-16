export default function (pi) {
  pi.registerProvider("lmstudio", {
    name: "LM Studio (local)",
    baseUrl: "http://127.0.0.1:1234/v1",
    apiKey: "lm-studio",
    api: "openai-completions",
    models: [
      {
        id: "qwen/qwen3.5-9b",
        name: "qwen/qwen3.5-9b",
        reasoning: false,
        input: ["text"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: 72000,
        maxTokens: 4096
      }
    ]
  });
}
