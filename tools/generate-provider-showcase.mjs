import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const iconBaseURL = "https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons";
const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outputPath = resolve(repositoryRoot, "docs/assets/provider-showcase.svg");

const providers = [
  ["Codex Subscription", "openai.svg"],
  ["OpenAI", "openai.svg"],
  ["Anthropic", "anthropic.svg"],
  ["Google Gemini", "gemini-color.svg"],
  ["Azure OpenAI", "azure-color.svg"],
  ["Amazon Bedrock", "bedrock-color.svg"],
  ["Google Vertex AI", "vertexai-color.svg"],
  ["xAI / Grok", "grok.svg"],
  ["DeepSeek", "deepseek-color.svg"],
  ["Qwen / DashScope", "qwen-color.svg"],
  ["Moonshot AI / Kimi", "moonshot.svg"],
  ["Z.AI / GLM", "zhipu-color.svg"],
  ["MiniMax", "minimax-color.svg"],
  ["Doubao", "doubao-color.svg"],
  ["SiliconFlow", "siliconcloud-color.svg"],
  ["ModelScope", "modelscope-color.svg"],
  ["OpenRouter", "openrouter-color.svg"],
  ["Groq", "groq.svg"],
  ["Together AI", "together-color.svg"],
  ["Fireworks AI", "fireworks-color.svg"],
  ["Mistral AI", "mistral-color.svg"],
  ["Cohere", "cohere-color.svg"],
  ["Perplexity", "perplexity-color.svg"],
  ["Hugging Face", "huggingface-color.svg"],
  ["NVIDIA NIM", "nvidia-color.svg"],
  ["GitHub Models", "github.svg"],
  ["GitHub Copilot", "githubcopilot.svg"],
  ["Vercel AI Gateway", "vercel.svg"],
  ["Cloudflare AI Gateway", "cloudflare-color.svg"],
  ["Ollama", "ollama.svg"],
  ["LM Studio", "lmstudio.svg"],
  ["vLLM / Custom", "vllm-color.svg"],
  ["Llama", "local:frontend/public/model-icons/llama.svg"],
];

function escapeXML(value) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

async function loadIcon(source) {
  if (source.startsWith("local:")) {
    return readFile(resolve(repositoryRoot, source.slice("local:".length)), "utf8");
  }

  const response = await fetch(`${iconBaseURL}/${source}`);
  if (!response.ok) {
    throw new Error(`Unable to download ${source}: ${response.status} ${response.statusText}`);
  }
  return response.text();
}

const columns = 5;
const tileWidth = 200;
const tileHeight = 96;
const width = columns * tileWidth;
const rows = Math.ceil(providers.length / columns);
const height = rows * tileHeight;
const icons = await Promise.all(providers.map(([, source]) => loadIcon(source)));

const tiles = providers.map(([name], index) => {
  const row = Math.floor(index / columns);
  const rowStart = row * columns;
  const itemsInRow = Math.min(columns, providers.length - rowStart);
  const column = index - rowStart;
  const rowOffset = ((columns - itemsInRow) * tileWidth) / 2;
  const centerX = rowOffset + column * tileWidth + tileWidth / 2;
  const iconData = Buffer.from(icons[index]).toString("base64");

  return [
    `  <g aria-label="${escapeXML(name)}">`,
    `    <image x="${centerX - 22}" y="${row * tileHeight + 16}" width="44" height="44" filter="url(#monochrome)" href="data:image/svg+xml;base64,${iconData}"/>`,
    `    <text x="${centerX}" y="${row * tileHeight + 78}" text-anchor="middle">${escapeXML(name)}</text>`,
    "  </g>",
  ].join("\n");
});

const svg = [
  '<?xml version="1.0" encoding="UTF-8"?>',
  `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img" aria-labelledby="title description">`,
  "  <title id=\"title\">Popular TokenHub provider integrations</title>",
  `  <desc id="description">${escapeXML(providers.map(([name]) => name).join(", "))}</desc>`,
  "  <defs>",
  "    <filter id=\"monochrome\" color-interpolation-filters=\"sRGB\">",
  "      <feColorMatrix values=\"0 0 0 0 0.39  0 0 0 0 0.42  0 0 0 0 0.44  0 0 0 1 0\"/>",
  "    </filter>",
  "  </defs>",
  `  <rect width="${width}" height="${height}" fill="#f5f6f4"/>`,
  "  <g fill=\"#6b7276\" font-family=\"ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace\" font-size=\"14\" font-weight=\"500\" letter-spacing=\"0\">",
  ...tiles,
  "  </g>",
  "</svg>",
  "",
].join("\n");

await mkdir(dirname(outputPath), { recursive: true });
await writeFile(outputPath, svg, "utf8");
console.log(`Generated ${outputPath}`);
