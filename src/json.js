// Preserve the API's uint64 IDs, including numbers beyond JavaScript's safe range.
export function parseJSON(text) {
  return JSON.parse(text, (_key, value, context) => {
    if (typeof value === 'number' && Number.isInteger(value) && !Number.isSafeInteger(value)) {
      if (!/^-?\d+$/.test(context?.source ?? '')) throw new Error('Unsafe integer literal; use full integer digits.');
      return BigInt(context.source);
    }
    return value;
  });
}
export function stringifyJSON(value, space) {
  return JSON.stringify(value, (_key, item) => typeof item === 'bigint' ? JSON.rawJSON(String(item)) : item, space);
}
