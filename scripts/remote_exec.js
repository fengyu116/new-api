#!/usr/bin/env node
/* Run a command on a remote host via password SSH. */

const fs = require('fs');
const net = require('net');
const path = require('path');
const { createRequire } = require('module');

function parseArgs(argv) {
  const args = {};
  for (let i = 2; i < argv.length; i += 1) {
    const item = argv[i];
    if (!item.startsWith('--')) continue;
    const key = item.slice(2);
    const next = argv[i + 1];
    if (!next || next.startsWith('--')) args[key] = true;
    else {
      args[key] = next;
      i += 1;
    }
  }
  return args;
}

function loadSsh2(moduleDir) {
  const candidates = [];
  if (moduleDir) candidates.push(path.resolve(moduleDir, 'ssh2'));
  candidates.push(path.resolve('tmp/ssh-tools/node_modules', 'ssh2'));
  candidates.push('ssh2');
  candidates.push('C:/Users/fengyu/AppData/Local/Temp/codex-ssh-client/node_modules/ssh2');
  for (const candidate of candidates) {
    try {
      if (path.isAbsolute(candidate)) {
        return createRequire(path.join(candidate, 'package.json'))('ssh2');
      }
      return require(candidate);
    } catch (_) {
      // keep looking
    }
  }
  throw new Error('Cannot load ssh2. Pass --ssh2-module-dir <node_modules>');
}

async function main() {
  const args = parseArgs(process.argv);
  const host = args.host;
  const username = args.user || 'root';
  const password = args.password || process.env.REMOTE_SSH_PASSWORD;
  const command = args['command-file']
    ? fs.readFileSync(args['command-file'], 'utf8')
    : args.command;
  if (!host || !password || !command) {
    console.error('Usage: node scripts/remote_exec.js --host <host> --user root --command "<command>"');
    process.exit(2);
  }
  const { Client } = loadSsh2(args['ssh2-module-dir']);
  const connectOptions = { host, username, password, readyTimeout: 20000 };
  if (args.proxy) {
    connectOptions.sock = await openProxySocket(args.proxy, host, Number(args.port || 22));
    delete connectOptions.host;
  }
  await new Promise((resolve, reject) => {
    const conn = new Client();
    conn.on('ready', () => {
      conn.exec(command, (err, stream) => {
        if (err) {
          conn.end();
          reject(err);
          return;
        }
        let stderr = '';
        stream
          .on('close', (code) => {
            conn.end();
            if (code === 0) resolve();
            else reject(new Error(`remote command exited with ${code}: ${stderr}`));
          })
          .on('data', (data) => process.stdout.write(data))
          .stderr.on('data', (data) => {
            stderr += data.toString();
            process.stderr.write(data);
          });
      });
    }).on('error', reject).connect(connectOptions);
  });
}

function openProxySocket(proxy, targetHost, targetPort) {
  const url = new URL(proxy);
  if (url.protocol !== 'http:') {
    throw new Error(`Unsupported proxy protocol: ${url.protocol}`);
  }
  return new Promise((resolve, reject) => {
    const socket = net.connect(Number(url.port || 80), url.hostname);
    let buffer = '';
    socket.setTimeout(20000);
    socket.on('connect', () => {
      socket.write(`CONNECT ${targetHost}:${targetPort} HTTP/1.1\r\nHost: ${targetHost}:${targetPort}\r\n\r\n`);
    });
    socket.on('data', function onData(chunk) {
      buffer += chunk.toString('binary');
      const idx = buffer.indexOf('\r\n\r\n');
      if (idx === -1) return;
      socket.off('data', onData);
      const header = buffer.slice(0, idx);
      if (!/^HTTP\/\d\.\d 200\b/.test(header)) {
        socket.destroy();
        reject(new Error(`Proxy CONNECT failed: ${header.split('\r\n')[0]}`));
        return;
      }
      const rest = Buffer.from(buffer.slice(idx + 4), 'binary');
      if (rest.length > 0) socket.unshift(rest);
      resolve(socket);
    });
    socket.on('timeout', () => {
      socket.destroy();
      reject(new Error('Proxy CONNECT timeout'));
    });
    socket.on('error', reject);
  });
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : err);
  process.exit(1);
});
