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
import { useMemo, useState, type ReactNode } from 'react'
import { AlertCircle, CheckCircle2, Upload } from 'lucide-react'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

type ProviderPreset = 'vector' | 'shengge' | 'custom'

type ProviderCatalogReport = {
  mode: string
  provider_code: string
  provider_name: string
  base_url: string
  models: number
  vendors: number
  groups: number
  channels_to_create: number
  channels_to_replace: number
  missing_key_groups?: string[]
  invalid_rows?: Array<{ row: number; field: string; message: string }>
  special_pricing_models: number
  skipped_special_models?: string[]
  changed_option_keys: string[]
  managed_tag_prefix: string
  applied: boolean
}

type ApiResponse<T> = {
  success: boolean
  message: string
  data: T
}

const PRESETS: Record<
  ProviderPreset,
  {
    providerCode: string
    providerName: string
    ruleType: string
    baseUrl: string
  }
> = {
  vector: {
    providerCode: 'vector',
    providerName: '向量',
    ruleType: 'vector_normal',
    baseUrl: '',
  },
  shengge: {
    providerCode: 'shengge',
    providerName: '胜哥',
    ruleType: 'shengge_normal',
    baseUrl: '',
  },
  custom: {
    providerCode: '',
    providerName: '',
    ruleType: 'vector_normal',
    baseUrl: '',
  },
}

const RULE_TYPES = [
  { value: 'vector_normal', label: '向量普通规则 JSON' },
  { value: 'vector_special', label: '向量特殊规则 JSON' },
  { value: 'shengge_normal', label: '胜哥普通规则 JSON' },
]

const APPLY_CONFIRM = 'APPLY_PROVIDER_CATALOG'

export function ProviderCatalogImportSection() {
  const [preset, setPreset] = useState<ProviderPreset>('vector')
  const [providerCode, setProviderCode] = useState(PRESETS.vector.providerCode)
  const [providerName, setProviderName] = useState(PRESETS.vector.providerName)
  const [ruleType, setRuleType] = useState(PRESETS.vector.ruleType)
  const [baseUrl, setBaseUrl] = useState('')
  const [ruleFile, setRuleFile] = useState<File | null>(null)
  const [keyFile, setKeyFile] = useState<File | null>(null)
  const [confirm, setConfirm] = useState('')
  const [report, setReport] = useState<ProviderCatalogReport | null>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState<'preview' | 'apply' | null>(null)

  const canSubmit = useMemo(
    () =>
      providerCode.trim() !== '' &&
      ruleType.trim() !== '' &&
      baseUrl.trim() !== '' &&
      ruleFile !== null,
    [baseUrl, providerCode, ruleFile, ruleType]
  )

  const applyPreset = (next: ProviderPreset) => {
    setPreset(next)
    const value = PRESETS[next]
    setProviderCode(value.providerCode)
    setProviderName(value.providerName)
    setRuleType(value.ruleType)
    if (value.baseUrl) setBaseUrl(value.baseUrl)
  }

  const buildForm = (mode: 'preview' | 'apply') => {
    const form = new FormData()
    form.append('provider_code', providerCode.trim())
    form.append('provider_name', providerName.trim())
    form.append('rule_type', ruleType.trim())
    form.append('base_url', baseUrl.trim())
    if (ruleFile) form.append('rule_file', ruleFile)
    if (keyFile) form.append('key_file', keyFile)
    if (mode === 'apply') form.append('confirm', confirm.trim())
    return form
  }

  const submit = async (mode: 'preview' | 'apply') => {
    setLoading(mode)
    setMessage('')
    try {
      const res = await api.post<ApiResponse<ProviderCatalogReport>>(
        `/api/provider-catalog/${mode}`,
        buildForm(mode)
      )
      if (!res.data.success) {
        setMessage(res.data.message || '导入失败')
        return
      }
      setReport(res.data.data)
      setMessage(mode === 'apply' ? '已写入数据库' : 'dry-run 预览已生成')
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '请求失败')
    } finally {
      setLoading(null)
    }
  }

  return (
    <div className='grid gap-4'>
      <Card>
        <CardHeader>
          <CardTitle>供应商目录导入</CardTitle>
          <CardDescription>
            普通规则、特殊规则、分组 key 分开上传；先 dry-run，确认后才写入。
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4'>
          <div className='grid gap-3 md:grid-cols-3'>
            <Field label='供应商预设'>
              <select
                className={selectClassName}
                value={preset}
                onChange={(event) =>
                  applyPreset(event.target.value as ProviderPreset)
                }
              >
                <option value='vector'>向量</option>
                <option value='shengge'>胜哥</option>
                <option value='custom'>其他</option>
              </select>
            </Field>
            <Field label='provider_code'>
              <Input
                value={providerCode}
                onChange={(event) => setProviderCode(event.target.value)}
                placeholder='vector'
              />
            </Field>
            <Field label='provider_name'>
              <Input
                value={providerName}
                onChange={(event) => setProviderName(event.target.value)}
                placeholder='向量'
              />
            </Field>
          </div>

          <div className='grid gap-3 md:grid-cols-2'>
            <Field label='规则类型'>
              <select
                className={selectClassName}
                value={ruleType}
                onChange={(event) => setRuleType(event.target.value)}
              >
                {RULE_TYPES.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))}
              </select>
            </Field>
            <Field label='base_url'>
              <Input
                value={baseUrl}
                onChange={(event) => setBaseUrl(event.target.value)}
                placeholder='https://api.example.com'
              />
            </Field>
          </div>

          <div className='grid gap-3 md:grid-cols-2'>
            <Field label='规则文件'>
              <Input
                type='file'
                accept='.txt,.json'
                onChange={(event) =>
                  setRuleFile(event.target.files?.[0] ?? null)
                }
              />
            </Field>
            <Field label='分组 key 文件（可选）'>
              <Input
                type='file'
                accept='.xlsx'
                onChange={(event) => setKeyFile(event.target.files?.[0] ?? null)}
              />
            </Field>
          </div>

          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              disabled={!canSubmit || loading !== null}
              onClick={() => submit('preview')}
            >
              <Upload />
              {loading === 'preview' ? '预览中...' : 'dry-run 预览'}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>确认写入</CardTitle>
          <CardDescription>
            写入只替换当前供应商标签下的渠道和能力，不会修改支付、充值倍率、用户余额、用户账号分组。
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          <Field label={`输入 ${APPLY_CONFIRM} 后允许 apply`}>
            <Textarea
              className='min-h-10'
              value={confirm}
              onChange={(event) => setConfirm(event.target.value)}
              placeholder={APPLY_CONFIRM}
            />
          </Field>
          <Button
            type='button'
            variant='destructive'
            disabled={
              !canSubmit || confirm.trim() !== APPLY_CONFIRM || loading !== null
            }
            onClick={() => submit('apply')}
          >
            {loading === 'apply' ? '写入中...' : '确认写入数据库'}
          </Button>
        </CardContent>
      </Card>

      {message && (
        <div
          className={cn(
            'flex items-center gap-2 rounded-lg border px-3 py-2 text-sm',
            report ? 'border-green-500/30 text-green-700' : 'border-red-500/30 text-red-700'
          )}
        >
          {report ? <CheckCircle2 className='size-4' /> : <AlertCircle className='size-4' />}
          {message}
        </div>
      )}

      {report && <ImportReportView report={report} />}
    </div>
  )
}

function Field({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className='grid gap-1.5'>
      <Label>{label}</Label>
      {children}
    </div>
  )
}

function ImportReportView({ report }: { report: ProviderCatalogReport }) {
  const items = [
    ['供应商', `${report.provider_name} (${report.provider_code})`],
    ['base_url', report.base_url],
    ['模型数', report.models],
    ['供应商模型厂商数', report.vendors],
    ['分组数', report.groups],
    ['将创建渠道', report.channels_to_create],
    ['将替换渠道', report.channels_to_replace],
    ['特殊规则模型', report.special_pricing_models],
    ['托管标签前缀', report.managed_tag_prefix],
  ]

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          导入报告
          <Badge variant={report.applied ? 'default' : 'secondary'}>
            {report.applied ? '已写入' : 'dry-run'}
          </Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className='grid gap-4'>
        <div className='grid gap-2 md:grid-cols-3'>
          {items.map(([label, value]) => (
            <div key={label} className='rounded-lg border p-3'>
              <div className='text-muted-foreground text-xs'>{label}</div>
              <div className='mt-1 break-all text-sm font-medium'>{value}</div>
            </div>
          ))}
        </div>

        <ReportList title='缺少 key 的分组' values={report.missing_key_groups} />
        <ReportList title='会更新的 Option' values={report.changed_option_keys} />
        <ReportList
          title='跳过的特殊规则模型'
          values={report.skipped_special_models}
        />

        {report.invalid_rows && report.invalid_rows.length > 0 && (
          <div className='grid gap-2'>
            <div className='font-medium'>分组 key 文件错误</div>
            <div className='grid gap-2'>
              {report.invalid_rows.map((row) => (
                <div key={`${row.row}-${row.field}`} className='rounded-lg border p-2 text-sm'>
                  第 {row.row} 行 / {row.field}: {row.message}
                </div>
              ))}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function ReportList({
  title,
  values,
}: {
  title: string
  values?: string[]
}) {
  if (!values || values.length === 0) {
    return null
  }
  return (
    <div className='grid gap-2'>
      <div className='font-medium'>{title}</div>
      <div className='flex flex-wrap gap-2'>
        {values.map((value) => (
          <Badge key={value} variant='outline' className='break-all'>
            {value}
          </Badge>
        ))}
      </div>
    </div>
  )
}

const selectClassName =
  'border-input focus-visible:border-ring focus-visible:ring-ring/50 dark:bg-input/30 h-8 w-full rounded-lg border bg-transparent px-2.5 py-1 text-sm outline-none focus-visible:ring-3'
