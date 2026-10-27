import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const { providers } = JSON.parse(await readFile(new URL('../data/provider-catalog.json', import.meta.url), 'utf8'));
const model = (provider, id) => {
  const entry = providers[provider].models.find(item => item.id === id);
  assert.ok(entry, `${provider}/${id} is missing`);
  return entry;
};

test('model pricing, lifecycle and access remain scoped to the exact offer', () => {
  const terra = model('openai', 'gpt-6.1-terra');
  assert.equal(terra.limit.context, 1050000);
  assert.equal(terra.limit.output, 128000);
  assert.deepEqual([terra.cost.input, terra.cost.output, terra.cost.cache_read, terra.cost.cache_write], [1, 5, .05, 1.25]);
  assert.equal(terra.metadata.pricing_status, 'unverified');
  assert.ok(terra.reasoning_options[0].values.includes('none'));
  for (const provider of ['moonshot', 'moonshot-ai']) {
    for (const id of ['kimi-k2.6', 'kimi-k2.7-code', 'kimi-k2.7-code-highspeed']) {
      const entry = model(provider, id);
      assert.equal(entry.metadata.new_user_restriction_date, '2026-10-31');
      assert.equal(entry.metadata.shutdown_at, undefined);
    }
  }
  assert.equal(model('kimi-for-coding', 'kimi-for-coding').metadata.new_user_restriction_date, undefined);
  assert.equal(model('kimi-for-coding', 'kimi-for-coding').cost, undefined);
  assert.equal(model('moonshot', 'kimi-k3.1-pro').cost, undefined);
  assert.equal(model('moonshot-ai', 'kimi-k3.1-pro').cost.input, 2);
  assert.equal(model('moonshot-ai', 'kimi-k3.1-pro').cost.cache_write, undefined);
  assert.equal(model('groq', 'groq/compound').metadata.shutdown_date, '2026-09-21');
  assert.equal(model('groq', 'groq/compound').metadata.shutdown_at, undefined);
  assert.equal(JSON.parse(model('mistral', 'mistral-large-4').metadata.reference_token_prices).input, 1.36);
});

test('new model and realtime session contracts remain catalog only', () => {
  for (const [provider, id] of [['moonshot','kimi-k3.1-pro'], ['moonshot-ai','kimi-k3.1-pro-highspeed'], ['kimi-for-coding','k3.1-pro'], ['alibaba','qwen-audio-3.1-realtime-flash'], ['alibaba-cn','qwen-audio-3.1-realtime-flash']]) {
    assert.equal(model(provider,id).metadata.call_support, 'unsupported');
  }
});
