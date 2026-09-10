import type { Command } from 'commander'
import process from 'node:process'

export function register(program: Command): void {
  program
    .command('zip [directory]')
    .description('Zip a directory into a zip file')
    .option('-w, --without-open', 'Do not open the zip file after creation')
    .option('-d, --with-dir <dir>', 'Include the directory name as a top-level folder in the zip')
    .action(async (directory: string | undefined, options: { withoutOpen?: boolean, withDir?: string }) => {
      const { zip } = await import('./zip')
      await zip({
        directory: directory ?? process.cwd(),
        open: !options.withoutOpen,
        withDir: options.withDir,
      })
    })
}
