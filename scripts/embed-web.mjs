import { cp, mkdir, readdir, rm } from 'node:fs/promises';

for (const destination of ['web/assets', 'android/app/src/main/assets/web']) {
  await mkdir(destination, { recursive: true });
  for (const entry of await readdir(destination)) {
    if (entry !== 'README.txt') await rm(`${destination}/${entry}`, { force: true, recursive: true });
  }
  await cp('dist', destination, { recursive: true });
}
console.log('Embedded frontend assets prepared for Go and Android.');
