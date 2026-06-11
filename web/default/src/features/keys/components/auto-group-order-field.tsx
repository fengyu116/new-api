/*
Copyright (C) 2023-2026 QuantumNous

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
import { ArrowDown, ArrowUp, Plus, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

type AutoGroupOrderFieldProps = {
  candidates: string[]
  value: string[]
  onChange: (value: string[]) => void
}

/**
 * 令牌级自定义 auto 候选分组顺序控件（二开新增）。
 * 选中列表按优先级排列，支持上移/下移/移除；候选区点击追加。
 * 留空表示使用全局策略顺序。
 */
export function AutoGroupOrderField({
  candidates,
  value,
  onChange,
}: AutoGroupOrderFieldProps) {
  const { t } = useTranslation()
  const selected = value.filter((g) => candidates.includes(g))
  const remaining = candidates.filter((g) => !selected.includes(g))

  const move = (index: number, delta: number) => {
    const next = [...selected]
    const target = index + delta
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    onChange(next)
  }

  return (
    <div className='flex flex-col gap-2'>
      {selected.length > 0 && (
        <ol className='flex flex-col gap-1.5'>
          {selected.map((group, index) => (
            <li
              key={group}
              className='border-input bg-muted/30 flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm'
            >
              <span className='text-muted-foreground w-5 shrink-0 text-xs tabular-nums'>
                {index + 1}.
              </span>
              <span className='min-w-0 flex-1 truncate'>{group}</span>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='size-6'
                disabled={index === 0}
                aria-label={t('Move up')}
                onClick={() => move(index, -1)}
              >
                <ArrowUp className='size-3.5' />
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='size-6'
                disabled={index === selected.length - 1}
                aria-label={t('Move down')}
                onClick={() => move(index, 1)}
              >
                <ArrowDown className='size-3.5' />
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='size-6'
                aria-label={t('Remove')}
                onClick={() => onChange(selected.filter((g) => g !== group))}
              >
                <X className='size-3.5' />
              </Button>
            </li>
          ))}
        </ol>
      )}
      {remaining.length > 0 && (
        <div className='flex flex-wrap gap-1.5'>
          {remaining.map((group) => (
            <Button
              key={group}
              type='button'
              variant='outline'
              size='sm'
              className='h-7 gap-1 px-2 text-xs'
              onClick={() => onChange([...selected, group])}
            >
              <Plus className='size-3' />
              {group}
            </Button>
          ))}
        </div>
      )}
    </div>
  )
}
