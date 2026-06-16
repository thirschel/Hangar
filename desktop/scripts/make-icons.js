// Generates the app + tray icons (no image deps): a dark terminal tile with a
// green "run" triangle. Writes build/icon.png (256, used by electron-builder to
// derive the Windows .ico) and build/tray.png (32, for the system tray).
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');

function crc32(buf) {
  let c = ~0;
  for (let i = 0; i < buf.length; i++) {
    c ^= buf[i];
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return (~c) >>> 0;
}

function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const typeBuf = Buffer.from(type, 'ascii');
  const crcBuf = Buffer.alloc(4);
  crcBuf.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])), 0);
  return Buffer.concat([len, typeBuf, data, crcBuf]);
}

function encodePng(size, pixels) {
  const sig = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // color type RGBA
  // bytes 10,11,12 = 0 (compression, filter, interlace)
  const raw = Buffer.alloc((size * 4 + 1) * size);
  for (let y = 0; y < size; y++) {
    raw[y * (size * 4 + 1)] = 0; // filter: none
    pixels.copy(raw, y * (size * 4 + 1) + 1, y * size * 4, (y + 1) * size * 4);
  }
  const idat = zlib.deflateSync(raw, { level: 9 });
  return Buffer.concat([sig, chunk('IHDR', ihdr), chunk('IDAT', idat), chunk('IEND', Buffer.alloc(0))]);
}

function draw(size) {
  const px = Buffer.alloc(size * size * 4);
  const set = (x, y, r, g, b, a) => {
    const i = (y * size + x) * 4;
    px[i] = r; px[i + 1] = g; px[i + 2] = b; px[i + 3] = a;
  };
  const bg = [30, 30, 30];
  const tile = [37, 37, 38];
  const green = [46, 160, 67];
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const m = size * 0.08;
      const inner = x >= m && x <= size - m && y >= m && y <= size - m;
      let c = inner ? tile : bg;

      // Green right-pointing "run" triangle.
      const xa = size * 0.38;
      const xb = size * 0.66;
      const cy = size * 0.5;
      if (x >= xa && x <= xb) {
        const t = (x - xa) / (xb - xa);
        const halfH = size * 0.18 * (1 - t);
        if (y >= cy - halfH && y <= cy + halfH) c = green;
      }
      set(x, y, c[0], c[1], c[2], 255);
    }
  }
  return px;
}

const outDir = path.resolve(__dirname, '..', 'build');
fs.mkdirSync(outDir, { recursive: true });
fs.writeFileSync(path.join(outDir, 'icon.png'), encodePng(256, draw(256)));
fs.writeFileSync(path.join(outDir, 'tray.png'), encodePng(32, draw(32)));
console.log('wrote build/icon.png (256) and build/tray.png (32)');
