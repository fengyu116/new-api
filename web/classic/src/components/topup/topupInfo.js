export function normalizeTopupInfo(data) {
  return {
    amount_options: data.amount_options || [],
    discount: data.discount || {},
    topup_group_ratio: Number(data.topup_group_ratio) || 1.0,
  };
}

export function calculateEffectiveTopupDiscount(
  presetDiscount,
  topupGroupRatio,
) {
  return presetDiscount * topupGroupRatio;
}
