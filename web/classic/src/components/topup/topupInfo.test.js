import assert from 'node:assert/strict';
import test from 'node:test';

import { normalizeTopupInfo } from './topupInfo.js';

test('preserves the account topup group ratio', () => {
  assert.deepEqual(
    normalizeTopupInfo({
      amount_options: [10, 20],
      discount: {},
      topup_group_ratio: 0.6,
    }),
    {
      amount_options: [10, 20],
      discount: {},
      topup_group_ratio: 0.6,
    },
  );
});
