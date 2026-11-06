#!/usr/bin/env bun
/**
 * scripts/check-web.js
 * Fast validator for web/src/*.js syntax, relative import integrity, and frontend tests.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';

// pxi: checks run on Bun, which has no --check flag; parse with its transpiler.
function checkSyntax(file) {
  if (typeof Bun !== 'undefined') {
    new Bun.Transpiler({ loader: 'js' }).transformSync(fs.readFileSync(file, 'utf8'));
    return;
  }
  execFileSync(process.execPath, ['--check', file], { stdio: 'pipe' });
}

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, '..');
const srcDir = path.join(rootDir, 'web', 'src');

console.log('[check-web] Validating frontend modules...');
const files = fs.readdirSync(srcDir).filter(f => f.endsWith('.js'));
let hasError = false;

for (const file of files) {
  const filePath = path.join(srcDir, file);
  try {
    checkSyntax(filePath);
  } catch (err) {
    console.error(`[check-web] Syntax error in ${file}:`, err.stderr?.toString() || err.message);
    hasError = true;
  }

  const content = fs.readFileSync(filePath, 'utf-8');
  const importRegex = /(?:import|from)\s+['"](\.[^'"]+)['"]/g;
  let match;
  while ((match = importRegex.exec(content)) !== null) {
    const relTarget = match[1];
    const resolved = path.resolve(srcDir, relTarget.endsWith('.js') ? relTarget : relTarget + '.js');
    if (!fs.existsSync(resolved)) {
      console.error(`[check-web] Broken import in ${file}: cannot resolve "${relTarget}"`);
      hasError = true;
    }
  }
}

try {
  execFileSync('bun', ['test', 'web/src/'], { cwd: rootDir, stdio: 'inherit' });
} catch (err) {
  // If bun test fails, propagate error
  if (err.status !== 0) {
    hasError = true;
  }
}

for (const bundle of ['web/app.js', 'web/workbench.js']) {
  const bundlePath = path.join(rootDir, bundle);
  if (fs.existsSync(bundlePath)) {
    try {
      checkSyntax(bundlePath);
    } catch (err) {
      console.error(`[check-web] Syntax error in ${bundle}:`, err.stderr?.toString() || err.message);
      hasError = true;
    }
  }
}

if (hasError) {
  process.exit(1);
}
console.log(`[check-web] Checked ${files.length} modules: all syntax, imports, and tests OK.`);
