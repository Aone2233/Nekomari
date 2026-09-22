import { ESLint } from 'eslint';
import hooks from 'eslint-plugin-react-hooks';

// Advisory audit, deliberately separate from the adopted CI rules. A nonzero
// result records migration work; it must not be mistaken for the release gate.
const engine = new ESLint({ overrideConfig: { rules: hooks.configs.recommended.rules } });
const results = await engine.lintFiles(['src']);
const formatter = await engine.loadFormatter('stylish');
process.stdout.write(formatter.format(results));
process.exitCode = results.some(result => result.messages.length > 0) ? 1 : 0;
