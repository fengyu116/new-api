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
import { Button, Space, Tag, Typography } from '@douyinfe/semi-ui';
import {
  IconArrowUp,
  IconArrowDown,
  IconClose,
  IconPlus,
} from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

/**
 * 令牌级自定义 auto 候选分组顺序控件（二开新增）。
 * value 为有序数组；candidates 为后端下发的可选 auto 分组。
 * 留空表示使用全局策略顺序。
 */
const AutoGroupOrderList = ({ candidates = [], value = [], onChange }) => {
  const { t } = useTranslation();
  const selected = value.filter((g) => candidates.includes(g));
  const remaining = candidates.filter((g) => !selected.includes(g));

  const move = (index, delta) => {
    const next = [...selected];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  return (
    <div className='flex flex-col gap-2'>
      {selected.map((group, index) => (
        <div
          key={group}
          className='flex items-center gap-2 rounded-lg px-2 py-1'
          style={{
            background: 'var(--semi-color-fill-0)',
            border: '1px solid var(--semi-color-border)',
          }}
        >
          <Text type='tertiary' size='small' style={{ width: 18 }}>
            {index + 1}.
          </Text>
          <Text className='flex-1' ellipsis={{ showTooltip: true }}>
            {group}
          </Text>
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
            disabled={index === selected.length - 1}
            onClick={() => move(index, 1)}
          />
          <Button
            theme='borderless'
            type='danger'
            size='small'
            icon={<IconClose />}
            onClick={() => onChange(selected.filter((g) => g !== group))}
          />
        </div>
      ))}
      {remaining.length > 0 && (
        <Space wrap>
          {remaining.map((group) => (
            <Tag
              key={group}
              color='white'
              type='ghost'
              className='cursor-pointer'
              prefixIcon={<IconPlus size='small' />}
              onClick={() => onChange([...selected, group])}
            >
              {group}
            </Tag>
          ))}
        </Space>
      )}
    </div>
  );
};

export default AutoGroupOrderList;
