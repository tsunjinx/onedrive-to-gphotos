"""Offline macOS launcher tests. Fake Go proves orchestration, not compilation."""
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FAKE_GO = '''#!/bin/bash
if [[ $1 == version ]]; then echo 'go version go1.27.2 darwin/arm64'; exit 0; fi
[[ ${FAIL_BUILD:-0} != 1 ]] || exit 1
while [[ $1 != -o ]]; do shift; done
shift
cat > "$1" <<'BIN'
#!/bin/bash
printf '%s\\n' "$@"
exit "${CLI_EXIT:-0}"
BIN
chmod +x "$1"
'''

class LauncherTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='od2gp tests ')
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.repo = self.base / 'repo with spaces'
        (self.repo / 'scripts').mkdir(parents=True)
        shutil.copy(ROOT / 'scripts/od2gp.sh', self.repo / 'scripts/od2gp.sh')
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        self.cache = self.base / 'tool cache'
        self.env = dict(os.environ, PATH=f'{self.bin}:/usr/bin:/bin', OD2GP_TOOL_DIR=str(self.cache))
        self.write_tool('uname', '#!/bin/bash\n[[ $1 == -s ]] && echo Darwin || echo arm64\n')
        self.archive = self.base / 'fixture.tar.gz'
        with tarfile.open(self.archive, 'w:gz') as tar:
            data = FAKE_GO.encode()
            item = tarfile.TarInfo('go/bin/go')
            item.size = len(data)
            item.mode = 0o755
            tar.addfile(item, io.BytesIO(data))
        self.sha = hashlib.sha256(self.archive.read_bytes()).hexdigest()
        self.manifest(self.sha)
        self.write_tool('curl', '#!/bin/bash\n[[ ${FAIL_DOWNLOAD:-0} != 1 ]] || exit 6\nwhile [[ $1 != --output ]]; do shift; done\nshift\ncp "$FIXTURE_ARCHIVE" "$1"\n')
        self.env['FIXTURE_ARCHIVE'] = str(self.archive)

    def write_tool(self, name, text):
        path = self.bin / name
        path.write_text(text)
        path.chmod(0o755)

    def manifest(self, checksum):
        (self.repo / 'scripts/toolchain.tsv').write_text(f'1.27.2 darwin arm64 go1.27.2.darwin-arm64.tar.gz {checksum}\n')

    def run_cli(self, *args):
        return subprocess.run(['/bin/bash', str(self.repo / 'scripts/od2gp.sh'), *args], env=self.env, capture_output=True, text=True, timeout=15)

    def test_compatible_go_forwards_arguments_and_exit_status(self):
        self.write_tool('go', FAKE_GO)
        self.env['CLI_EXIT'] = '7'
        result = self.run_cli('argument with spaces', '--flag')
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ['argument with spaces', '--flag'])
        self.assertFalse(self.cache.exists())

    def test_missing_go_installs_then_reuses_offline(self):
        first = self.run_cli('version')
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertTrue((self.cache / 'go1.27.2-darwin-arm64/go/bin/go').exists())
        self.env['FAIL_DOWNLOAD'] = '1'
        second = self.run_cli('help')
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertNotIn('Installing', second.stderr)
        self.assertEqual(list(self.cache.glob('.install.*')), [])

    def test_outdated_go_is_preserved(self):
        self.write_tool('go', '#!/bin/bash\necho "go version go1.20.1 darwin/arm64"\n')
        original = (self.bin / 'go').read_bytes()
        result = self.run_cli('help')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.bin / 'go').read_bytes(), original)

    def test_checksum_mismatch_never_executes_archive(self):
        self.manifest('0' * 64)
        result = self.run_cli('help')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('checksum mismatch', result.stderr)
        self.assertFalse((self.cache / 'go1.27.2-darwin-arm64').exists())
        self.assertFalse((self.cache / '.install-lock').exists())

    def test_download_failure_cleans_owned_stage(self):
        self.env['FAIL_DOWNLOAD'] = '1'
        result = self.run_cli('help')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Download failed', result.stderr)
        self.assertEqual(list(self.cache.iterdir()), [])

    def test_competing_lock_is_preserved(self):
        (self.cache / '.install-lock').mkdir(parents=True)
        result = self.run_cli('help')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Another setup', result.stderr)
        self.assertTrue((self.cache / '.install-lock').exists())

    def test_broken_go_installs_private_toolchain(self):
        self.write_tool('go', '#!/bin/bash\nexit 1\n')
        result = self.run_cli('help')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Installing', result.stderr)

    def test_unsupported_platform_does_not_install(self):
        self.write_tool('uname', '#!/bin/bash\necho Linux\n')
        result = self.run_cli('help')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('macOS only', result.stderr)
        self.assertFalse(self.cache.exists())

    def test_invalid_managed_install_is_not_overwritten(self):
        managed = self.cache / 'go1.27.2-darwin-arm64'
        managed.mkdir(parents=True)
        marker = managed / 'keep.txt'
        marker.write_text('existing data')
        result = self.run_cli('help')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(), 'existing data')

    def test_build_failure_does_not_launch(self):
        self.write_tool('go', FAKE_GO)
        self.env['FAIL_BUILD'] = '1'
        result = self.run_cli('should-not-print')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, '')
        self.assertEqual(list((self.repo / '.cache').iterdir()), [])

if __name__ == '__main__':
    unittest.main()
