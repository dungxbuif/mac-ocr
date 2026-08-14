// Starts both local dependencies with one command and forwards shutdown
// signals so neither child process is left behind.
const { spawn } = require('child_process');
const path = require('path');

const root = __dirname;

const children = [
  spawn('npm', ['--prefix', 's3', 'start'], { cwd: root, stdio: 'inherit', env: process.env }),
  spawn('go', ['run', '.'], { cwd: path.join(root, 'native'), stdio: 'inherit', env: process.env }),
];

let shuttingDown = false;

function shutdown(signal = 'SIGTERM') {
  if (shuttingDown) return;
  shuttingDown = true;
  for (const child of children) {
    if (!child.killed) child.kill(signal);
  }
}

for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => shutdown(signal));
}

for (const child of children) {
  child.on('error', (error) => {
    console.error(`failed to start local dependency: ${error.message}`);
    shutdown();
    process.exitCode = 1;
  });
  child.on('exit', (code, signal) => {
    if (!shuttingDown && code !== 0) {
      console.error(`local dependency exited (code=${code}, signal=${signal || 'none'})`);
      shutdown();
      process.exitCode = code || 1;
    }
    if (children.every((item) => item.exitCode !== null || item.signalCode !== null)) {
      process.exit();
    }
  });
}
