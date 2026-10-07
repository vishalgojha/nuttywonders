/*
 * Minimal QR encoder, byte mode, error correction level L.
 *
 * It exists so the WhatsApp pairing code never gets handed to a third party
 * image service: that string is a live credential for the next few seconds and
 * has no business travelling anywhere. Self contained, no dependencies, and it
 * only implements what the pairing flow needs.
 *
 * Cross checked module for module against the python `qrcode` library, and
 * round tripped through a real decoder (see tests/qr.check.js).
 */
(function (global) {
  'use strict';

  // version -> [blocks, codewords per block, data codewords per block] per
  // group, for error correction level L. Values are from ISO/IEC 18004.
  var VERSIONS = {
    1: [[1, 26, 19]], 2: [[1, 44, 34]], 3: [[1, 70, 55]], 4: [[1, 100, 80]],
    5: [[1, 134, 108]], 6: [[2, 86, 68]], 7: [[2, 98, 78]], 8: [[2, 121, 97]],
    9: [[2, 146, 116]], 10: [[2, 86, 68], [2, 87, 69]], 11: [[4, 101, 81]],
    12: [[2, 116, 92], [2, 117, 93]], 13: [[4, 133, 107]], 14: [[3, 145, 115], [1, 146, 116]],
    15: [[5, 109, 87], [1, 110, 88]], 16: [[5, 122, 98], [1, 123, 99]],
    17: [[1, 135, 107], [5, 136, 108]], 18: [[5, 150, 120], [1, 151, 121]],
    19: [[3, 141, 113], [4, 142, 114]], 20: [[3, 135, 107], [5, 136, 108]],
    21: [[4, 144, 116], [4, 145, 117]], 22: [[2, 139, 111], [7, 140, 112]],
    23: [[4, 151, 121], [5, 152, 122]], 24: [[6, 147, 117], [4, 148, 118]],
    25: [[8, 132, 106], [4, 133, 107]], 26: [[10, 142, 114], [2, 143, 115]],
    27: [[8, 152, 122], [4, 153, 123]],
  };

  // Row/column centres of the alignment patterns, per version.
  var ALIGNMENT = {
    1: [], 2: [6, 18], 3: [6, 22], 4: [6, 26], 5: [6, 30], 6: [6, 34],
    7: [6, 22, 38], 8: [6, 24, 42], 9: [6, 26, 46], 10: [6, 28, 50],
    11: [6, 30, 54], 12: [6, 32, 58], 13: [6, 34, 62], 14: [6, 26, 46, 66],
    15: [6, 26, 48, 70], 16: [6, 26, 50, 74], 17: [6, 30, 54, 78],
    18: [6, 30, 56, 82], 19: [6, 30, 58, 86], 20: [6, 34, 62, 90],
    21: [6, 28, 50, 72, 94], 22: [6, 26, 50, 74, 98], 23: [6, 30, 54, 78, 102],
    24: [6, 28, 54, 80, 106], 25: [6, 32, 58, 84, 110], 26: [6, 30, 58, 86, 114],
    27: [6, 34, 62, 90, 118],
  };

  var MAX_VERSION = 27;
  // Format information encodes the error correction level as two bits.
  var ECC_FORMAT_BITS = 1; // level L
  var PAD_BYTES = [0xec, 0x11];

  /* ---- GF(256) arithmetic, primitive polynomial 0x11d ------------------ */
  var EXP = new Uint8Array(512);
  var LOG = new Uint8Array(256);
  (function () {
    var x = 1;
    for (var i = 0; i < 255; i++) {
      EXP[i] = x;
      LOG[x] = i;
      x <<= 1;
      if (x & 0x100) x ^= 0x11d;
    }
    for (var j = 255; j < 512; j++) EXP[j] = EXP[j - 255];
  })();

  function multiply(a, b) {
    if (a === 0 || b === 0) return 0;
    return EXP[LOG[a] + LOG[b]];
  }

  // Generator polynomial for `degree` error correction codewords, in ascending
  // coefficient order: result[j] is the coefficient of x^j and the leading
  // coefficient is 1.
  function generatorPoly(degree) {
    var poly = [1];
    for (var i = 0; i < degree; i++) {
      var next = new Array(poly.length + 1).fill(0);
      for (var j = 0; j < poly.length; j++) {
        next[j] ^= multiply(poly[j], EXP[i]); // times the constant alpha^i
        next[j + 1] ^= poly[j];               // times x
      }
      poly = next;
    }
    return poly;
  }

  var generatorCache = {};

  function errorCorrection(data, degree) {
    if (!generatorCache[degree]) generatorCache[degree] = generatorPoly(degree);
    var gen = generatorCache[degree];
    var remainder = new Uint8Array(degree);

    // Synthetic division of the data polynomial by the generator polynomial.
    // gen is in ascending order with the leading coefficient (1) at index
    // degree, so the divisor's x^(degree-1) .. x^0 coefficients are read back
    // to front.
    for (var i = 0; i < data.length; i++) {
      var factor = data[i] ^ remainder[0];
      remainder.copyWithin(0, 1);
      remainder[degree - 1] = 0;
      if (factor !== 0) {
        for (var j = 0; j < degree; j++) {
          remainder[j] ^= multiply(gen[degree - 1 - j], factor);
        }
      }
    }
    return remainder;
  }

  /* ---- bit stream -------------------------------------------------------- */
  function BitBuffer() {
    this.bits = [];
  }

  BitBuffer.prototype.push = function (value, length) {
    for (var i = length - 1; i >= 0; i--) this.bits.push((value >>> i) & 1);
  };

  BitBuffer.prototype.length = function () {
    return this.bits.length;
  };

  BitBuffer.prototype.toBytes = function () {
    var bytes = new Uint8Array(Math.ceil(this.bits.length / 8));
    for (var i = 0; i < this.bits.length; i++) {
      if (this.bits[i]) bytes[i >> 3] |= 0x80 >> (i & 7);
    }
    return bytes;
  };

  function dataCodewordCount(version) {
    var total = 0;
    VERSIONS[version].forEach(function (group) {
      total += group[0] * group[2];
    });
    return total;
  }

  // Smallest version that fits `byteLength` bytes in byte mode at level L.
  function chooseVersion(byteLength) {
    for (var version = 1; version <= MAX_VERSION; version++) {
      var countBits = version < 10 ? 8 : 16;
      // 4 mode bits, the character count, then the payload itself.
      var needed = 4 + countBits + byteLength * 8;
      if (needed <= dataCodewordCount(version) * 8) return version;
    }
    throw new Error('payload of ' + byteLength + ' bytes is too long for a QR code');
  }

  function buildCodewords(bytes, version) {
    var buffer = new BitBuffer();
    buffer.push(0b0100, 4); // byte mode
    buffer.push(bytes.length, version < 10 ? 8 : 16);
    for (var i = 0; i < bytes.length; i++) buffer.push(bytes[i], 8);

    var capacity = dataCodewordCount(version) * 8;
    // Terminator: up to four zero bits, then pad to a byte boundary.
    buffer.push(0, Math.min(4, capacity - buffer.length()));
    while (buffer.length() % 8 !== 0) buffer.bits.push(0);

    var codewords = Array.prototype.slice.call(buffer.toBytes());
    var index = 0;
    while (codewords.length < dataCodewordCount(version)) {
      codewords.push(PAD_BYTES[index % 2]);
      index++;
    }
    return codewords;
  }

  // Split the data into blocks, add error correction, then interleave both
  // halves the way the standard requires.
  function interleave(codewords, version) {
    var dataBlocks = [];
    var ecBlocks = [];
    var offset = 0;

    VERSIONS[version].forEach(function (group) {
      var blocks = group[0];
      var total = group[1];
      var dataLength = group[2];
      for (var i = 0; i < blocks; i++) {
        var block = codewords.slice(offset, offset + dataLength);
        offset += dataLength;
        dataBlocks.push(block);
        ecBlocks.push(Array.prototype.slice.call(errorCorrection(Uint8Array.from(block), total - dataLength)));
      }
    });

    var result = [];
    var longest = Math.max.apply(null, dataBlocks.map(function (b) { return b.length; }));
    for (var d = 0; d < longest; d++) {
      dataBlocks.forEach(function (block) {
        if (d < block.length) result.push(block[d]);
      });
    }
    var longestEc = Math.max.apply(null, ecBlocks.map(function (b) { return b.length; }));
    for (var e = 0; e < longestEc; e++) {
      ecBlocks.forEach(function (block) {
        if (e < block.length) result.push(block[e]);
      });
    }
    return result;
  }

  /* ---- matrix ------------------------------------------------------------ */
  function Matrix(version) {
    this.version = version;
    this.size = version * 4 + 17;
    this.modules = [];
    this.reserved = [];
    for (var y = 0; y < this.size; y++) {
      this.modules.push(new Uint8Array(this.size));
      this.reserved.push(new Uint8Array(this.size));
    }
  }

  Matrix.prototype.set = function (row, col, dark) {
    if (row < 0 || col < 0 || row >= this.size || col >= this.size) return;
    this.modules[row][col] = dark ? 1 : 0;
    this.reserved[row][col] = 1;
  };

  Matrix.prototype.isReserved = function (row, col) {
    return this.reserved[row][col] === 1;
  };

  Matrix.prototype.get = function (row, col) {
    return this.modules[row][col];
  };

  function placeFinder(matrix, top, left) {
    // 7x7 pattern plus its separator, drawn light on the outside.
    for (var r = -1; r <= 7; r++) {
      for (var c = -1; c <= 7; c++) {
        var row = top + r;
        var col = left + c;
        if (row < 0 || col < 0 || row >= matrix.size || col >= matrix.size) continue;
        var inRing = (r >= 0 && r <= 6 && (c === 0 || c === 6)) ||
                     (c >= 0 && c <= 6 && (r === 0 || r === 6));
        var inCore = r >= 2 && r <= 4 && c >= 2 && c <= 4;
        matrix.set(row, col, inRing || inCore);
      }
    }
  }

  function placeAlignment(matrix, row, col) {
    for (var r = -2; r <= 2; r++) {
      for (var c = -2; c <= 2; c++) {
        var ring = Math.max(Math.abs(r), Math.abs(c));
        matrix.set(row + r, col + c, ring !== 1);
      }
    }
  }

  function placeFunctionPatterns(matrix) {
    var size = matrix.size;

    placeFinder(matrix, 0, 0);
    placeFinder(matrix, 0, size - 7);
    placeFinder(matrix, size - 7, 0);

    var centres = ALIGNMENT[matrix.version] || [];
    for (var a = 0; a < centres.length; a++) {
      for (var b = 0; b < centres.length; b++) {
        var row = centres[a];
        var col = centres[b];
        // Skip the three that would land on a finder pattern.
        if ((row === 6 && col === 6) ||
            (row === 6 && col === size - 7) ||
            (row === size - 7 && col === 6)) continue;
        placeAlignment(matrix, row, col);
      }
    }

    // Timing patterns.
    for (var i = 8; i < size - 8; i++) {
      matrix.set(6, i, i % 2 === 0);
      matrix.set(i, 6, i % 2 === 0);
    }

    // Reserve the format information areas; the real bits are written last.
    for (var f = 0; f <= 8; f++) {
      if (f !== 6) {
        matrix.set(8, f, 0);
        matrix.set(f, 8, 0);
      }
    }
    for (var g = 0; g < 8; g++) {
      matrix.set(8, size - 1 - g, 0);
      matrix.set(size - 1 - g, 8, 0);
    }
    matrix.set(size - 8, 8, 1); // dark module

    if (matrix.version >= 7) {
      for (var v = 0; v < 18; v++) {
        var a2 = Math.floor(v / 3);
        var b2 = size - 11 + (v % 3);
        matrix.set(a2, b2, 0);
        matrix.set(b2, a2, 0);
      }
    }
  }

  var MASK_FUNCTIONS = [
    function (row, col) { return (row + col) % 2 === 0; },
    function (row) { return row % 2 === 0; },
    function (row, col) { return col % 3 === 0; },
    function (row, col) { return (row + col) % 3 === 0; },
    function (row, col) { return (Math.floor(row / 2) + Math.floor(col / 3)) % 2 === 0; },
    function (row, col) { return ((row * col) % 2) + ((row * col) % 3) === 0; },
    function (row, col) { return ((((row * col) % 2) + ((row * col) % 3)) % 2) === 0; },
    function (row, col) { return ((((row + col) % 2) + ((row * col) % 3)) % 2) === 0; },
  ];

  function placeData(matrix, codewords) {
    var size = matrix.size;
    var bitIndex = 0;

    function nextBit() {
      if (bitIndex >= codewords.length * 8) return 0; // remainder bits are light
      var bit = (codewords[bitIndex >> 3] >> (7 - (bitIndex & 7))) & 1;
      bitIndex++;
      return bit;
    }

    var upward = true;
    for (var right = size - 1; right >= 1; right -= 2) {
      // Column 6 is the vertical timing pattern and is skipped entirely.
      if (right === 6) right = 5;
      for (var step = 0; step < size; step++) {
        var row = upward ? size - 1 - step : step;
        for (var colOffset = 0; colOffset < 2; colOffset++) {
          var col = right - colOffset;
          if (matrix.isReserved(row, col)) continue;
          matrix.modules[row][col] = nextBit();
        }
      }
      upward = !upward;
    }
  }

  function applyMask(matrix, maskIndex, reservedPattern) {
    var fn = MASK_FUNCTIONS[maskIndex];
    for (var row = 0; row < matrix.size; row++) {
      for (var col = 0; col < matrix.size; col++) {
        if (reservedPattern[row][col]) continue;
        if (fn(row, col)) matrix.modules[row][col] ^= 1;
      }
    }
  }

  function penalty(matrix) {
    var size = matrix.size;
    var score = 0;

    // Rule 1: runs of five or more same coloured modules.
    function scanLine(get) {
      var run = 1;
      var first = get(0);
      for (var i = 1; i < size; i++) {
        if (get(i) === first) {
          run++;
        } else {
          if (run >= 5) score += run - 2;
          run = 1;
          first = get(i);
        }
      }
      if (run >= 5) score += run - 2;
    }
    for (var r = 0; r < size; r++) {
      scanLine((function (row) {
        return function (col) { return matrix.get(row, col); };
      })(r));
      scanLine((function (col) {
        return function (row) { return matrix.get(row, col); };
      })(r));
    }

    // Rule 2: every 2x2 block of one colour.
    for (var y = 0; y < size - 1; y++) {
      for (var x = 0; x < size - 1; x++) {
        var value = matrix.get(y, x);
        if (value === matrix.get(y, x + 1) &&
            value === matrix.get(y + 1, x) &&
            value === matrix.get(y + 1, x + 1)) {
          score += 3;
        }
      }
    }

    // Rule 3: finder-like patterns, scored for every occurrence.
    var FINDER_PATTERNS = [
      [1, 0, 1, 1, 1, 0, 1, 0, 0, 0, 0],
      [0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1],
    ];
    function matches(get, start, pattern) {
      for (var i = 0; i < pattern.length; i++) {
        if (get(start + i) !== pattern[i]) return false;
      }
      return true;
    }
    // Once module 10 is dark only the trailing-light pattern can match, and it
    // cannot overlap itself, so the next possible match is two modules on.
    function scanFinderLike(get) {
      var total = 0;
      for (var i = 0; i <= size - 11; i++) {
        for (var p = 0; p < FINDER_PATTERNS.length; p++) {
          if (matches(get, i, FINDER_PATTERNS[p])) {
            total += 40;
            break;
          }
        }
        if (get(i + 10)) i++;
      }
      return total;
    }
    for (var row2 = 0; row2 < size; row2++) {
      score += scanFinderLike((function (currentRow) {
        return function (col) { return matrix.get(currentRow, col); };
      })(row2));
    }
    for (var col2 = 0; col2 < size; col2++) {
      score += scanFinderLike((function (currentCol) {
        return function (row) { return matrix.get(row, currentCol); };
      })(col2));
    }

    // Rule 4: deviation from an even split of dark and light.
    var dark = 0;
    for (var d = 0; d < size; d++) {
      for (var e = 0; e < size; e++) dark += matrix.get(d, e);
    }
    var percent = (dark * 100) / (size * size);
    var stepSize = Math.floor(Math.abs(percent - 50) / 5) * 10;
    score += stepSize;

    return score;
  }

  // Polynomial long division of (data << k) by the BCH generator, which is
  // cheaper to get right than a shift register and matches ISO/IEC 18004
  // Annex B exactly. `data` is 5 bits, so the dividend spans 5 + degree bits.
  function bchRemainder(data, generator, degree) {
    var rem = data << degree;
    for (var i = degree + 4; i >= degree; i--) {
      if (rem & (1 << i)) rem ^= generator << (i - degree);
    }
    return rem & ((1 << degree) - 1);
  }

  function formatBits(maskIndex) {
    var data = (ECC_FORMAT_BITS << 3) | maskIndex;
    return ((data << 10) | bchRemainder(data, 0x537, 10)) ^ 0x5412;
  }

  function versionBits(version) {
    return (version << 12) | bchRemainder(version, 0x1f25, 12);
  }

  function drawFormatInfo(matrix, maskIndex) {
    var size = matrix.size;
    var bits = formatBits(maskIndex);

    // First copy: bit 0 runs down column 8, wrapping into row 8 and then back
    // along row 8 to the left edge.
    for (var i = 0; i <= 5; i++) matrix.set(i, 8, (bits >>> i) & 1);
    matrix.set(7, 8, (bits >>> 6) & 1);
    matrix.set(8, 8, (bits >>> 7) & 1);
    matrix.set(8, 7, (bits >>> 8) & 1);
    for (var j = 9; j < 15; j++) matrix.set(8, 14 - j, (bits >>> j) & 1);

    // Second copy: bits 0-7 run left along row 8, bits 8-14 run up column 8.
    for (var k = 0; k < 8; k++) matrix.set(8, size - 1 - k, (bits >>> k) & 1);
    for (var l = 8; l < 15; l++) matrix.set(size - 7 + (l - 8), 8, (bits >>> l) & 1);

    matrix.set(size - 8, 8, 1); // always-dark module
  }

  function drawVersionInfo(matrix) {
    if (matrix.version < 7) return;
    var size = matrix.size;
    var bits = versionBits(matrix.version);
    for (var i = 0; i < 18; i++) {
      var bit = (bits >>> i) & 1;
      var top = Math.floor(i / 3);
      var side = size - 11 + (i % 3);
      matrix.set(top, side, bit);
      matrix.set(side, top, bit);
    }
  }

  function copyMatrix(matrix) {
    var clone = new Matrix(matrix.version);
    for (var row = 0; row < matrix.size; row++) {
      clone.modules[row].set(matrix.modules[row]);
      clone.reserved[row].set(matrix.reserved[row]);
    }
    return clone;
  }

  function buildMatrix(text, version, maskIndex) {
    var bytes = toBytes(text);
    var codewords = interleave(buildCodewords(bytes, version), version);

    var base = new Matrix(version);
    placeFunctionPatterns(base);
    placeData(base, codewords);

    var matrix = copyMatrix(base);
    applyMask(matrix, maskIndex, base.reserved);
    drawFormatInfo(matrix, maskIndex);
    drawVersionInfo(matrix);
    return matrix;
  }

  function toBytes(text) {
    return Array.prototype.slice.call(new TextEncoder().encode(String(text)));
  }

  /**
   * Encode text as a QR code.
   * Returns { size, version, mask, modules } where modules is an array of
   * row arrays holding 1 for a dark module and 0 for a light one.
   */
  function encode(text, options) {
    var opts = options || {};
    var version = opts.version || chooseVersion(toBytes(text).length);

    if (opts.mask !== undefined) {
      var pinned = buildMatrix(text, version, opts.mask);
      return {
        size: pinned.size,
        version: version,
        mask: opts.mask,
        modules: pinned.modules.map(function (row) { return Array.prototype.slice.call(row); }),
      };
    }

    // Score all eight masks and keep the lowest penalty. The score covers the
    // finished symbol, format information included.
    var best = null;
    for (var maskIndex = 0; maskIndex < 8; maskIndex++) {
      var candidate = buildMatrix(text, version, maskIndex);
      var score = penalty(candidate);
      if (!best || score < best.score) {
        best = { score: score, matrix: candidate, maskIndex: maskIndex };
      }
    }

    return {
      size: best.matrix.size,
      version: version,
      mask: best.maskIndex,
      modules: best.matrix.modules.map(function (row) { return Array.prototype.slice.call(row); }),
    };
  }

  /**
   * Penalty score of the finished symbol under one specific mask. Exposed so
   * the cross check in tests/qr.check.js can hold mask selection to a reference
   * implementation instead of trusting it.
   */
  function penaltyOf(text, options) {
    var opts = options || {};
    var version = opts.version || chooseVersion(toBytes(text).length);
    return penalty(buildMatrix(text, version, opts.mask || 0));
  }

  /**
   * Render a QR code as an SVG path string. Callers scale it themselves with
   * the quiet zone built in.
   */
  function toSvgPath(text, options) {
    var opts = options || {};
    var quiet = opts.quiet === undefined ? 4 : opts.quiet;
    var scale = opts.scale || 4;
    var code = encode(text, opts);
    var side = (code.size + quiet * 2) * scale;

    var path = [];
    for (var row = 0; row < code.size; row++) {
      for (var col = 0; col < code.size; col++) {
        if (code.modules[row][col]) {
          path.push('M' + ((col + quiet) * scale) + ' ' + ((row + quiet) * scale) +
                    'h' + scale + 'v' + scale + 'h-' + scale + 'z');
        }
      }
    }

    return {
      svg: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ' + side + ' ' + side +
           '" width="' + side + '" height="' + side + '" shape-rendering="crispEdges">' +
           '<rect width="' + side + '" height="' + side + '" fill="#ffffff"/>' +
           '<path d="' + path.join('') + '" fill="#1d300f"/></svg>',
      version: code.version,
      mask: code.mask,
      size: code.size,
    };
  }

  global.nwQR = {
    encode: encode,
    toSvgPath: toSvgPath,
    chooseVersion: chooseVersion,
    penaltyOf: penaltyOf,
  };
})(typeof window !== 'undefined' ? window : globalThis);
