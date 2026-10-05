const http = require('node:http');

const port = Number(process.env.PORT || 3001);
const allowedOrigin = process.env.CORS_ORIGIN || '*';

function send(res, status, payload) {
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Access-Control-Allow-Origin': allowedOrigin,
    'Access-Control-Allow-Headers': 'Content-Type, Authorization',
    'Access-Control-Allow-Methods': 'GET, POST, OPTIONS'
  });
  res.end(JSON.stringify(payload));
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let body = '';
    req.on('data', chunk => { body += chunk; if (body.length > 100000) reject(new Error('Payload too large')); });
    req.on('end', () => { try { resolve(body ? JSON.parse(body) : {}); } catch { reject(new Error('Invalid JSON')); } });
    req.on('error', reject);
  });
}

const server = http.createServer(async (req, res) => {
  if (req.method === 'OPTIONS') return send(res, 204, {});
  if (req.method === 'GET' && req.url === '/health') return send(res, 200, { ok: true, service: 'nuttywonders-api' });
  if (req.method === 'GET' && req.url === '/api') return send(res, 200, { name: 'NuttyWonders API', version: '0.1.0', endpoints: ['/health', '/api/cost/calculate'] });
  if (req.method === 'POST' && req.url === '/api/cost/calculate') {
    try {
      const input = await readBody(req);
      const ingredients = Number(input.ingredients || 0);
      const packaging = Number(input.packaging || 0);
      const labour = Number(input.labour || 0);
      const overhead = Number(input.overhead || 0);
      const quantity = Math.max(1, Number(input.quantity || 1));
      const price = Number(input.price || 0);
      const targetMargin = Math.min(95, Math.max(1, Number(input.targetMargin || 45)));
      const batchCost = ingredients + packaging + labour + overhead;
      const unitCost = batchCost / quantity;
      const suggestedPrice = unitCost / (1 - targetMargin / 100);
      const margin = price > 0 ? ((price - unitCost) / price) * 100 : null;
      return send(res, 200, { batchCost, unitCost, suggestedPrice, margin, targetMargin, quantity, currency: 'INR' });
    } catch (error) { return send(res, 400, { error: error.message }); }
  }
  if (req.method === 'POST' && req.url === '/api/ai/chat') {
    try {
      if (!process.env.OPENROUTER_API_KEY) return send(res, 503, { error: 'AI is not configured yet' });
      const input = await readBody(req);
      const message = String(input.message || '').trim();
      if (!message) return send(res, 400, { error: 'Message is required' });
      const response = await fetch('https://openrouter.ai/api/v1/chat/completions', {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${process.env.OPENROUTER_API_KEY}`,
          'Content-Type': 'application/json',
          'HTTP-Referer': process.env.OPENROUTER_SITE_URL || 'https://app.nuttywonders.com',
          'X-Title': 'NuttyWonders Studio'
        },
        body: JSON.stringify({
          model: process.env.OPENROUTER_MODEL || 'openrouter/free',
          messages: [
            { role: 'system', content: 'You are NuttyWonders Cost Copilot. Help a small Indian snack business calculate batch cost, unit cost, selling price, gross margin, and simple product decisions. Use INR (₹), show concise maths, and never invent inventory or orders. If numbers are missing, ask for them.' },
            { role: 'user', content: message }
          ],
          temperature: 0.2,
          max_tokens: 350
        })
      });
      const payload = await response.json();
      if (!response.ok) return send(res, response.status, { error: payload?.error?.message || 'OpenRouter request failed' });
      return send(res, 200, { reply: payload?.choices?.[0]?.message?.content || 'I could not calculate that just yet.' });
    } catch (error) { return send(res, 502, { error: error.message }); }
  }
  send(res, 404, { error: 'Not found' });
});

server.listen(port, '0.0.0.0', () => console.log(`NuttyWonders API listening on ${port}`));
