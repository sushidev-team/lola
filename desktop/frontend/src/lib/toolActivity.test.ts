import { describe, it, expect } from 'vitest';
import { activityTool, sourceReadPaths } from './toolActivity';
const run = (summary: string, status = 'ok') => activityTool({ name: 'Bash', category: 'run', summary, status } as never);

describe('activity names', () => {
  it('names a commit after checkout setup and staging', () => {
    expect(run('cd "/a b"; git add src/view.ts && git commit -m "fix: keep ; punctuation"')).toMatchObject({ name: 'Commit', summary: 'fix: keep ; punctuation' });
    expect(run('cd /repo && git -C /repo push origin main')).toMatchObject({ name: 'Push', summary: 'git push origin main' });
  });
  it('names explicit file operations and preserves the command', () => {
    expect(run('cd /repo; rm -f "src/old view.ts"')).toMatchObject({ name: 'Delete', category: 'edit', summary: 'src/old view.ts', detail: 'cd /repo; rm -f "src/old view.ts"' });
    expect(run('mv src/old.ts src/new.ts')).toMatchObject({ name: 'Move', summary: 'src/old.ts → src/new.ts' });
  });
  it('does not call quoted text or an uncompleted chain a file change', () => {
    expect(run('echo "rm -f a.ts; git commit"').name).toBe('Bash');
    expect(run('git diff src/view.ts').category).toBe('run');
    expect(run('make check && git commit -m fix', 'error').name).toBe('Bash');
    expect(run('git commit --dry-run').name).toBe('Bash');
  });
  it('removes setup from a raw shell label without losing the original', () => {
    const command = 'cd /repo; SP=/repo/src/view.tsx; rg -n Button "$SP"';
    expect(run(command)).toMatchObject({ summary: 'rg -n Button "$SP"', detail: command });
  });
});


describe('source reads', () => {
  it('recognizes the multi-file sed/cut read from the activity stream', () => {
    const command = "sed -n '972,996p' tests/Feature/WorkspaceTest.php | cut -c1-170; sed -n '232,240p' tests/browser/workflow.php";
    expect(sourceReadPaths(command)).toEqual(['tests/Feature/WorkspaceTest.php', 'tests/browser/workflow.php']);
    expect(run(command)).toMatchObject({ category: 'read', name: 'Read', detail: command });
  });
  it('does not mistake writes, scripts or mixed command output for file reads', () => {
    for (const command of ['sed -i 1,3p app.ts', "sed -n '1,3p;w out.ts' app.ts", 'cat a.ts > b.ts', 'cat a.ts; npm test', 'echo a.ts', 'git diff a.ts']) {
      expect(sourceReadPaths(command)).toEqual([]);
    }
  });
});
