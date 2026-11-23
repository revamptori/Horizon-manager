import { execFileSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';

await mkdir('release', { recursive: true });
for (const [arch, label] of [['amd64', 'x64'], ['arm64', 'arm64']]) {
  execFileSync('go', ['build', '-trimpath', '-ldflags=-s -w -H=windowsgui', '-o', `release/Horizon-Manager-windows-${label}.exe`, './cmd/horizon'], {
    stdio: 'inherit', env: { ...process.env, GOOS: 'windows', GOARCH: arch, CGO_ENABLED: '0' },
  });
}
