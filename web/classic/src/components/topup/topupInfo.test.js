import assert from 'node:assert/strict';
import test from 'node:test';

import {
  calculateEffectiveTopupDiscount,
  normalizeTopupInfo,
} from './topupInfo.js';

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

test('combines preset discount with account topup group ratio', () => {
  assert.equal(calculateEffectiveTopupDiscount(1, 0.6), 0.6);
  assert.equal(calculateEffectiveTopupDiscount(0.8, 0.6), 0.48);
});
