// Cross checks qr.js against the python `qrcode` library.
//
// Two independent things are checked:
//
//   1. Module for module equality. For a fixed version and mask the encoder has
//      no freedom left, so the matrices must be byte identical. This is the
//      check that actually catches encoder bugs, and it runs for all 27
//      supported versions.
//
//   2. Mask selection. The lowest-penalty mask is chosen by scoring the
//      finished symbol, which is what ISO/IEC 18004 describes. python-qrcode
//      scores a matrix with the format information blanked instead, so its own
//      choice is recomputed here from the final matrices before comparing.
//
// Run: node tests/qr.check.js <python with the qrcode package installed>
const { execFileSync } = require('node:child_process');
const path = require('node:path');
const { readFileSync, writeFileSync, mkdtempSync } = require('node:fs');
const os = require('node:os');

const HERE = __dirname;
const ROOT = path.resolve(HERE, '..');

function loadEncoder() {
  const source = readFileSync(path.join(ROOT, 'qr.js'), 'utf8');
  const vm = require('node:vm');
  const sandbox = { TextEncoder, Uint8Array, Math, Array, JSON };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  return sandbox.nwQR;
}

const encoder = loadEncoder();
if (!encoder) throw new Error('qr.js did not export nwQR');

const PYTHON = process.argv[2] || 'python3';

const reference = `
import json, sys
import qrcode
from qrcode import constants, util

def encode(text, version, mask):
    qr = qrcode.QRCode(error_correction=constants.ERROR_CORRECT_L, box_size=1, border=0)
    qr.version = version
    qr.mask_pattern = mask
    # Force byte mode so the reference cannot pick numeric or alphanumeric.
    qr.add_data(util.QRData(text, mode=util.MODE_8BIT_BYTE))
    qr.make(fit=False)
    rows = qr.get_matrix()
    return qr.version, ["".join("1" if c else "0" for c in row) for row in rows]

def pick_version(text):
    qr = qrcode.QRCode(error_correction=constants.ERROR_CORRECT_L, box_size=1, border=0)
    qr.add_data(util.QRData(text, mode=util.MODE_8BIT_BYTE))
    qr.best_fit()
    return qr.version

out = []
for entry in json.loads(sys.stdin.read()):
    mask = entry.get("mask")
    if mask is not None:
        version, rows = encode(entry["text"], entry.get("version") or 1, mask)
        out.append({"version": version, "masks": [{"mask": mask, "rows": rows, "score": None}]})
        continue

    # No mask pinned: score all eight finished symbols and keep the lowest,
    # ties going to the lowest mask number.
    version = pick_version(entry["text"])
    candidates = []
    for m in range(8):
        _, rows = encode(entry["text"], version, m)
        grid = [[c == "1" for c in row] for row in rows]
        candidates.append({"mask": m, "rows": rows, "score": util.lost_point(grid)})
    best = min(range(8), key=lambda i: (candidates[i]["score"], i))
    out.append({"version": version, "masks": candidates, "best": best})

json.dump(out, sys.stdout)
`;

const dir = mkdtempSync(path.join(os.tmpdir(), 'qrcheck-'));
const script = path.join(dir, 'ref.py');
writeFileSync(script, reference);

// A spread of realistic and adversarial payloads, including the character
// counts that straddle a version boundary.
const payloads = [
  { text: 'A' },
  { text: 'https://nuttywonders.com' },
  { text: 'NuttyWonders pairing secret' },
  { text: '2@oP3s5rQ1zW8xY7bN4mK6jH9gF2dS0aV8cE5uT3wR7yI1qZ6xC4bN9mL2kJ8hG5fD0' },
  { text: 'x'.repeat(100) },
  { text: 'x'.repeat(230) },
  { text: 'x'.repeat(300) },
  { text: 'x'.repeat(500) },
  { text: 'x'.repeat(700) },
  { text: 'The quick brown fox jumps over the lazy dog. 0123456789' },
  { text: 'A'.repeat(45) }, { text: 'A'.repeat(46) }, { text: 'A'.repeat(47) },
  { text: 'B'.repeat(134) }, { text: 'B'.repeat(135) }, { text: 'B'.repeat(136) },
  { text: 'C'.repeat(271) }, { text: 'C'.repeat(272) }, { text: 'C'.repeat(273) },
];

// Also pin every version with a fixed mask so placement is checked for all of
// them, not just the ones these lengths happen to hit.
for (let version = 1; version <= 27; version++) {
  payloads.push({ text: 'V' + version + '-' + 'z'.repeat(version * 11), version, mask: 3 });
}

const input = JSON.stringify(payloads);
let output;
try {
  output = execFileSync(PYTHON, [script], { input, maxBuffer: 64 * 1024 * 1024 }).toString();
} catch (err) {
  const detail = (err.stderr || '').toString() || err.message;
  console.error(`Could not run the reference encoder with ${PYTHON}:\n${detail.trim()}`);
  console.error('\nInstall the reference in a throwaway environment:');
  console.error('  python3 -m venv /tmp/qrvenv && /tmp/qrvenv/bin/pip install qrcode opencv-python-headless');
  console.error(`  node tests/qr.check.js /tmp/qrvenv/bin/python`);
  process.exit(1);
}
const expected = JSON.parse(output);

function firstDiffs(gotRows, wantRows, limit) {
  const diffs = [];
  for (let r = 0; r < wantRows.length && diffs.length < limit; r++) {
    for (let c = 0; c < wantRows[r].length; c++) {
      if (gotRows[r][c] !== wantRows[r][c]) {
        diffs.push(`(${r},${c}) got ${gotRows[r][c]} want ${wantRows[r][c]}`);
      }
    }
  }
  return diffs;
}

let failures = 0;
let checked = 0;

payloads.forEach((entry, i) => {
  const want = expected[i];
  if (!want) return; // the reference refused this payload, nothing to compare

  const got = encoder.encode(entry.text, { version: entry.version, mask: entry.mask });
  const gotRows = got.modules.map((row) => row.join(''));
  const label = `"${entry.text.slice(0, 20)}"${entry.text.length > 20 ? ` (${entry.text.length} chars)` : ''}`;
  checked++;

  if (got.version !== want.version) {
    console.log(`FAIL [${i}] version ${got.version} != ${want.version} for ${label}`);
    failures++;
    return;
  }

  // Which mask is expected, and the reference matrix for it.
  const expectedMask = entry.mask !== undefined ? entry.mask : want.masks[want.best].mask;
  const reference = want.masks.find((candidate) => candidate.mask === expectedMask);

  if (got.mask !== expectedMask) {
    console.log(`FAIL [${i}] mask ${got.mask} != ${expectedMask} for ${label}`);
    failures++;
    return;
  }
  if (gotRows.length !== reference.rows.length || gotRows.some((row, r) => row !== reference.rows[r])) {
    console.log(`FAIL [${i}] matrix differs (v${got.version} mask ${got.mask}) for ${label}`);
    console.log('   first diffs: ' + firstDiffs(gotRows, reference.rows, 6).join(', '));
    failures++;
    return;
  }

  // For unpinned masks, hold the whole penalty table to the reference too, so
  // a scoring regression cannot hide behind an unchanged choice.
  if (entry.mask === undefined) {
    for (let mask = 0; mask < 8; mask++) {
      const mine = encoder.penaltyOf(entry.text, { version: want.version, mask });
      const theirs = want.masks[mask].score;
      if (mine !== theirs) {
        console.log(`FAIL [${i}] mask ${mask} penalty ${mine} != ${theirs} for ${label}`);
        failures++;
      }
    }
  }
});

console.log(`${checked - failures}/${checked} QR encodings match the reference library`);

// Independent proof that the modules are scannable: matching a reference
// encoder proves the bits are right, a decoder proves a camera agrees.
//
// The decoder cannot read every symbol (it gives up on some dense high version
// ones, and so does the reference output), so each symbol is only required to
// decode when the reference's own matrix for it does. That keeps decoder
// limits out of the results without letting them hide an encoder regression.
const decoder = path.join(HERE, 'qr.decode.py');
const toDecode = payloads.map((entry, i) => {
  const want = expected[i];
  if (!want) return null;
  const expectedMask = entry.mask !== undefined ? entry.mask : want.masks[want.best].mask;
  const got = encoder.encode(entry.text, { version: entry.version, mask: entry.mask });
  return {
    text: entry.text,
    mine: got.modules.map((row) => row.join('')),
    theirs: want.masks.find((candidate) => candidate.mask === expectedMask).rows,
  };
}).filter(Boolean);

let decoded;
try {
  decoded = JSON.parse(
    execFileSync(PYTHON, [decoder], {
      input: JSON.stringify(toDecode.flatMap((entry) => [{ rows: entry.mine }, { rows: entry.theirs }])),
      maxBuffer: 64 * 1024 * 1024,
    }).toString()
  );
} catch (err) {
  console.log('SKIP decode round trip (needs opencv-python: ' + err.message.split('\n')[0] + ')');
}

if (decoded) {
  let decodeFailures = 0;
  let decodable = 0;
  toDecode.forEach((entry, i) => {
    const mine = decoded[i * 2];
    const theirs = decoded[i * 2 + 1];

    if (theirs === entry.text) {
      decodable++;
      if (mine !== entry.text) {
        console.log(`FAIL decode [${i}] got ${JSON.stringify(mine)} want ${JSON.stringify(entry.text.slice(0, 20))}`);
        decodeFailures++;
      }
    } else if (mine === entry.text) {
      // Reading it when the reference could not is fine, not a regression.
      decodable++;
    }
  });
  console.log(`${decodable - decodeFailures}/${decodable} QR encodings decode back to the original text`);
  failures += decodeFailures;
}

if (failures > 0) process.exit(1);
