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
import { Avatar, Typography, Table, Tag } from '@douyinfe/semi-ui';
import { IconCoinMoneyStroked } from '@douyinfe/semi-icons';
import { calculateModelPrice, getModelPriceItems } from '../../../../../helpers';

const { Text } = Typography;

const ModelPricingTable = ({
  modelData,
  groupRatio,
  currency,
  siteDisplayType,
  tokenUnit,
  displayPrice,
  showRatio,
  usableGroup,
  autoGroups = [],
  t,
}) => {
  const modelEnableGroups = Array.isArray(modelData?.enable_groups)
    ? modelData.enable_groups
    : [];
  const autoChain = autoGroups.filter((g) => modelEnableGroups.includes(g));
  const renderSpecialPricingTable = () => {
    const special = modelData?.special_pricing;
    if (!special || !Array.isArray(special.rows) || special.rows.length === 0) {
      if (!Array.isArray(special?.sections) || special.sections.length === 0) {
        return null;
      }
    }
    const availableGroups = Object.keys(usableGroup || {})
      .filter((g) => g !== '')
      .filter((g) => g !== 'auto')
      .filter((g) => modelEnableGroups.includes(g));
    const groups = availableGroups.length > 0 ? availableGroups : ['default'];
    const unitPrice = Number(special.credit_unit_price || 0);
    const sections =
      Array.isArray(special.sections) && special.sections.length > 0
        ? special.sections
        : [
            {
              title: special.title,
              description: special.description,
              unit: special.unit,
              columns: special.columns,
              rows: special.rows,
            },
          ];
    const formatRowPrice = (row, ratio, sectionUnit) => {
      if (Number(row.first_second_price || 0) > 0) {
        return `${t('第1秒')}${displayPrice(Number(row.first_second_price || 0) * ratio)}，${t('后续每秒')}+${displayPrice(Number(row.next_second_price || 0) * ratio)}`;
      }
      const price =
        Number(row.price || 0) > 0
          ? Number(row.price || 0) * ratio
          : unitPrice * Number(row.multiplier || 0) * ratio;
      return price > 0 ? `${displayPrice(price)} / ${t(row.unit || sectionUnit || special.unit || '次')}` : '-';
    };

    return (
      <div className='mb-5'>
        <div className='text-sm font-medium mb-1'>
          {special.title || t('特殊规格价格')}
        </div>
        {special.description && (
          <div className='text-xs text-gray-500 mb-2'>
            {special.description}
            {!special.billing_enabled ? ` · ${t('仅展示，未启用特殊扣费')}` : ''}
          </div>
        )}
        <div className='space-y-4'>
          {groups.map((group) => {
            const ratio = groupRatio && groupRatio[group] ? groupRatio[group] : 1;
            return (
              <div key={group} className='rounded-lg bg-gray-50 p-3'>
                <div className='mb-3 flex items-center gap-2'>
                  <Tag color='white' size='small' shape='circle'>
                    {group}
                    {t('分组')}
                  </Tag>
                  <Tag color='blue' size='small' shape='circle'>
                    {ratio}x
                  </Tag>
                </div>
                {sections.map((section, sectionIndex) => {
                  const columns = [
                    ...(section.columns || [])
                      .filter((col) => col.key !== 'price')
                      .map((col) => ({
                        title: t(col.title || col.key),
                        dataIndex: col.key,
                        render: (text) => text || '-',
                      })),
                    {
                      title: t('价格'),
                      dataIndex: 'price',
                      render: (_, row) => (
                        <div className='font-semibold text-orange-600'>
                          {formatRowPrice(row, ratio, section.unit)}
                        </div>
                      ),
                    },
                  ];
                  const tableData = (section.rows || []).map((row, index) => ({
                    key: `${group}-${sectionIndex}-${index}`,
                    ...row,
                  }));
                  return (
                    <div key={`${group}-${section.title || sectionIndex}`} className='mb-4 last:mb-0'>
                      <div className='mb-2 text-sm font-semibold'>
                        {section.title || special.title || t('特殊规格价格')}
                      </div>
                      {section.description && (
                        <div className='text-xs text-gray-500 mb-2'>
                          {section.description}
                        </div>
                      )}
                      <Table
                        dataSource={tableData}
                        columns={columns}
                        pagination={false}
                        size='small'
                        bordered={false}
                        className='!rounded-lg'
                      />
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>
      </div>
    );
  };

  const renderGroupPriceTable = () => {
    if (modelData?.special_pricing) {
      return null;
    }
    // 仅展示模型可用的分组：模型 enable_groups 与用户可用分组的交集

    const availableGroups = Object.keys(usableGroup || {})
      .filter((g) => g !== '')
      .filter((g) => g !== 'auto')
      .filter((g) => modelEnableGroups.includes(g));

    // 准备表格数据
    const tableData = availableGroups.map((group) => {
      const priceData = modelData
        ? calculateModelPrice({
            record: modelData,
            selectedGroup: group,
            groupRatio,
            tokenUnit,
            displayPrice,
            currency,
            quotaDisplayType: siteDisplayType,
          })
        : { inputPrice: '-', outputPrice: '-', price: '-' };

      // 获取分组倍率
      const groupRatioValue =
        groupRatio && groupRatio[group] ? groupRatio[group] : 1;

      return {
        key: group,
        group: group,
        ratio: groupRatioValue,
        billingType:
          modelData?.billing_mode === 'tiered_expr'
            ? t('动态计费')
            : modelData?.quota_type === 0
              ? t('按量计费')
              : modelData?.quota_type === 1
                ? t('按次计费')
                : '-',
        priceItems: getModelPriceItems(priceData, t, siteDisplayType),
      };
    });

    // 定义表格列
    const columns = [
      {
        title: t('分组'),
        dataIndex: 'group',
        render: (text) => (
          <Tag color='white' size='small' shape='circle'>
            {text}
            {t('分组')}
          </Tag>
        ),
      },
    ];

    const isDynamic = modelData?.billing_mode === 'tiered_expr';

    // 动态计费时始终显示倍率列，否则根据设置
    if (showRatio || isDynamic) {
      columns.push({
        title: t('分组倍率'),
        dataIndex: 'ratio',
        render: (text) => (
          <Tag color='blue' size='small' shape='circle'>
            {text}x
          </Tag>
        ),
      });
    }

    columns.push({
      title: t('计费类型'),
      dataIndex: 'billingType',
      render: (text) => {
        let color = 'white';
        if (text === t('按量计费')) color = 'violet';
        else if (text === t('按次计费')) color = 'teal';
        else if (text === t('动态计费')) color = 'amber';
        return (
          <Tag color={color} size='small' shape='circle'>
            {text || '-'}
          </Tag>
        );
      },
    });

    columns.push({
      title: siteDisplayType === 'TOKENS' ? t('计费摘要') : t('价格摘要'),
      dataIndex: 'priceItems',
      render: (items) => {
        if (items.length === 1 && items[0].isDynamic) {
          return (
            <Text type='tertiary' size='small'>
              {t('见上方动态计费详情')}
            </Text>
          );
        }
        return (
          <div className='space-y-1'>
            {items.map((item) => (
              <div key={item.key}>
                <div className='font-semibold text-orange-600'>
                  {item.label} {item.value}
                </div>
                <div className='text-xs text-gray-500'>{item.suffix}</div>
              </div>
            ))}
          </div>
        );
      },
    });

    return (
      <Table
        dataSource={tableData}
        columns={columns}
        pagination={false}
        size='small'
        bordered={false}
        className='!rounded-lg'
      />
    );
  };

  return (
    <div>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='orange' className='mr-2 shadow-md'>
          <IconCoinMoneyStroked size={16} />
        </Avatar>
        <div>
          <Text className='text-lg font-medium'>{t('分组价格')}</Text>
          <div className='text-xs text-gray-600'>
            {t('不同用户分组的价格信息')}
          </div>
        </div>
      </div>
      {autoChain.length > 0 && (
        <div className='flex flex-wrap items-center gap-1 mb-4'>
          <span className='text-sm text-gray-600'>{t('auto分组调用链路')}</span>
          <span className='text-sm'>→</span>
          {autoChain.map((g, idx) => (
            <React.Fragment key={g}>
              <Tag color='white' size='small' shape='circle'>
                {g}
                {t('分组')}
              </Tag>
              {idx < autoChain.length - 1 && <span className='text-sm'>→</span>}
            </React.Fragment>
          ))}
        </div>
      )}
      {renderSpecialPricingTable()}
      {renderGroupPriceTable()}
    </div>
  );
};

export default ModelPricingTable;
