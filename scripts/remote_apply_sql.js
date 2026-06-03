#!/usr/bin/env node
/* Apply a generated SQL file to a remote new-api postgres container via SSH. */

const fs = require('fs');
const path = require('path');
const { createRequire } = require('module');

function parseArgs(argv) {
  const args = {};
  for (let i = 2; i < argv.length; i += 1) {
    const item = argv[i];
    if (!item.startsWith('--')) continue;
    const key = item.slice(2);
    const next = argv[i + 1];
    if (!next || next.startsWith('--')) {
      args[key] = true;
    } else {
      args[key] = next;
      i += 1;
    }
  }
  return args;
}

function loadSsh2(moduleDir) {
  const candidates = [];
  if (moduleDir) candidates.push(path.resolve(moduleDir, 'ssh2'));
  candidates.push('ssh2');
  candidates.push('C:/Users/fengyu/AppData/Local/Temp/codex-ssh-client/node_modules/ssh2');

  for (const candidate of candidates) {
    try {
      if (path.isAbsolute(candidate)) {
        return createRequire(path.join(candidate, 'package.json'))('ssh2');
      }
      return require(candidate);
    } catch (_) {
      // try next candidate
    }
  }
  throw new Error('Cannot load ssh2. Install it or pass --ssh2-module-dir <node_modules>');
}

async function main() {
  const args = parseArgs(process.argv);
  const sqlPath = args.sql;
  const host = args.host;
  const username = args.user || 'root';
  const password = args.password || process.env.REMOTE_SSH_PASSWORD;
  const port = Number(args.port || 22);
  const remoteProject = args['remote-project'] || '/opt/new-api';
  const postgresService = args['postgres-service'] || 'postgres';
  const dbUser = args['db-user'] || 'root';
  const dbName = args['db-name'] || 'new-api';
  const backup = Boolean(args.backup);

  if (!sqlPath || !host || !password) {
    console.error('Usage: node scripts/remote_apply_sql.js --sql tmp/fzbl-import/fzbl-import.sql --host 154.12.60.218 --user root --password <password>');
    process.exit(2);
  }

  const sql = fs.readFileSync(sqlPath);
  const { Client } = loadSsh2(args['ssh2-module-dir']);
  const cdProject = `cd '${remoteProject.replace(/'/g, `'\\''`)}'`;
  const backupCommand = [
    cdProject,
    `docker compose exec -T '${postgresService}' pg_dump -U '${dbUser}' '${dbName}' > 'backup-fzbl-before-import-'$(date +%F-%H%M%S)'.sql'`,
  ].join(' && ');
  const importCommand = [
    cdProject,
    `docker compose exec -T '${postgresService}' psql -v ON_ERROR_STOP=1 -U '${dbUser}' -d '${dbName}'`,
  ].join(' && ');

  await new Promise((resolve, reject) => {
    const conn = new Client();
    conn
      .on('ready', () => {
        const run = (command, input, callback) => {
          conn.exec(command, (err, stream) => {
            if (err) {
              callback(err);
              return;
            }
            let stderr = '';
            stream
              .on('close', (code) => {
                if (code === 0) callback();
                else callback(new Error(`remote command exited with ${code}: ${stderr}`));
              })
              .on('data', (data) => process.stdout.write(data))
              .stderr.on('data', (data) => {
                stderr += data.toString();
                process.stderr.write(data);
              });
            if (input) stream.end(input);
            else stream.end();
          });
        };

        const importSql = () => run(importCommand, sql, (err) => {
          if (err) {
            conn.end();
            reject(err);
            return;
          }
          conn.end();
          resolve();
        });

        if (backup) {
          run(backupCommand, null, (err) => {
            if (err) {
              conn.end();
              reject(err);
              return;
            }
            importSql();
          });
        } else {
          importSql();
        }
      })
      .on('error', reject)
      .connect({ host, port, username, password, readyTimeout: 20000 });
  });
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : err);
  process.exit(1);
});
