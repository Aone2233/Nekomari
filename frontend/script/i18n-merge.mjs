// i18n 合并策略，单独一个模块是为了可测。
//
// 这段逻辑原本在 i18n-sync.mjs 里，而那个脚本一被 import 就会跑 main()，所以
// 它删键的行为没有任何测试看得见。现在并集语义本身有测试钉住
// （script/i18n-parity.test.mjs）。

/**
 * 合并一个目标语言与源语言。
 *
 * **并集语义**：源里的每个键都保留（目标有值就用目标的值），目标里独有、源里没有
 * 的键也保留。
 *
 * 这里以前只遍历 source 的键，于是目标语言独有的键会被静默删除。在没有
 * OPENAI_API_KEY 的环境里 aiTranslateAll 对每一对都返回空串，所以跑一次 sync 的
 * 实际效果就是「把三种语言里源语言没有的键全部删掉」。一个没有 API key 的人不该
 * 因为运行这个脚本而丢数据。
 *
 * @param {Record<string, string>} sourceFlat 展平后的源语言
 * @param {Record<string, string>} targetFlat 展平后的目标语言
 */
export function diffAndMerge(sourceFlat, targetFlat) {
  const result = {};
  const added = [];
  const updated = [];
  const keptOnlyInTarget = [];

  for (const key of Object.keys(sourceFlat)) {
    if (key in targetFlat) {
      const val = targetFlat[key];
      if (typeof val === 'string' && val.trim() === '') {
        // 空串视为缺失：留给 AI 补，或保持缺失，而不是写入空值。
        added.push(key);
      } else {
        result[key] = val;
      }
    } else {
      added.push(key);
    }
  }
  for (const key of Object.keys(targetFlat)) {
    if (!(key in sourceFlat)) {
      result[key] = targetFlat[key];
      keptOnlyInTarget.push(key);
    }
  }
  return { result, added, removed: [], keptOnlyInTarget, updated };
}
