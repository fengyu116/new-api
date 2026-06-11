/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React from 'react';
import { Button, Tag, Typography } from '@douyinfe/semi-ui';
import { IconArrowUp, IconArrowDown, IconClose } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

/**
 * 令牌分组优先级顺序列表（二开新增）。
 * value 为有序分组名数组；meta 为分组名到 { desc, ratio } 的映射。
 * 与上方的分组多选联动，仅负责调序与移除。
 */
const AutoGroupOrderList = ({ value = [], meta = {}, onChange }) => {
  const { t } = useTranslation();

  const move = (index, delta) => {
    const next = [...value];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  if (value.length === 0) {
    return null;
  }

  return (
    <div className='flex flex-col gap-2'>
      {value.map((group, index) => {
        const info = meta[group] || {};
        return (
          <div
            key={group}
            className='flex items-center gap-2 rounded-lg px-2 py-1.5'
            style={{
              background: 'var(--semi-color-fill-0)',
              border: '1px solid var(--semi-color-border)',
            }}
          >
            <Tag size='small' color='blue' className='shrink-0'>
              {t('优先级')} {index + 1}
            </Tag>
            <Text strong className='shrink-0'>
              {group}
            </Text>
            {info.desc && info.desc !== group && (
              <Text
                type='tertiary'
                size='small'
                className='flex-1 min-w-0'
                ellipsis={{ showTooltip: true }}
              >
                {info.desc}
              </Text>
            )}
            {(info.desc === undefined || info.desc === group) && (
              <span className='flex-1' />
            )}
            {info.ratio !== undefined && info.ratio !== '' && (
              <Tag size='small' color='green' className='shrink-0'>
                {info.ratio}
                {t('倍')}
              </Tag>
            )}
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              icon={<IconArrowUp />}
              disabled={index === 0}
              onClick={() => move(index, -1)}
            />
            <Button
              theme='borderless'
              type='tertiary'
              size='small'
              icon={<IconArrowDown />}
              disabled={index === value.length - 1}
              onClick={() => move(index, 1)}
            />
            <Button
              theme='borderless'
              type='danger'
              size='small'
              icon={<IconClose />}
              onClick={() => onChange(value.filter((g) => g !== group))}
            />
          </div>
        );
      })}
    </div>
  );
};

export default AutoGroupOrderList;
