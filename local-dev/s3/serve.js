// Lightweight local S3-compatible server for OCR API development.
//
// Starts s3rver on the port from S3_PORT (default 9000), rooted at ./data,
// and provisions the configured buckets on boot. Path-style addressing is the
// default, matching the proxy's S3_FORCE_PATH_STYLE=true.
const S3rver = require('s3rver');
const fs = require('fs');
const path = require('path');

const PORT = parseInt(process.env.S3_PORT || '9000', 10);
const ROOT = path.join(__dirname, 'data');
const BUCKETS = (process.env.S3_BUCKETS || 'macocr-inputs,macocr-results')
  .split(',')
  .filter(Boolean)
  .map((name) => ({ name }));

fs.mkdirSync(ROOT, { recursive: true });

const server = new S3rver({
  port: PORT,
  address: '127.0.0.1',
  silent: false,
  directory: ROOT,
  configureBuckets: BUCKETS,
  // Accept any credentials in local dev; s3rver has no user registry.
  allowMismatchedSignatures: true,
});

server
  .run()
  .then(() => {
    const { address, port } = server.serverOptions;
    console.log(`s3rver listening on http://${address}:${port}`);
    console.log(`buckets: ${BUCKETS.map((b) => b.name).join(', ')}`);
  })
  .catch((err) => {
    console.error(err);
    process.exit(1);
  });

process.on('SIGINT', () => process.exit(0));
process.on('SIGTERM', () => process.exit(0));
