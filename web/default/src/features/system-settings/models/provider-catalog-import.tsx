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

type ProviderPreset = 'vector' | 'fiveTwoOne' | 'shengge' | 'custom'

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
  group_conflict_mode?: string
  group_name_mappings?: Record<string, string>
  special_pricing_models: number
  task_billing_rules: number
  tiered_billing_models: number
  remote_pricing_report?: {
    checked_models: number
    mismatches?: Array<{
      model_name?: string
      group?: string
      field: string
      local?: unknown
      remote?: unknown
    }>
    remote_only_models?: string[]
  }
  blocked_reasons?: string[]
  source_hashes?: Record<string, string>
  previous_source_hashes?: Record<string, string>
  source_hashes_changed: boolean
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

type BillingAuditReport = {
  scanned: number
  affected: number
  difference_quota: number
  dry_run: boolean
  rows: Array<{
    task_id: string
    user_id: number
    model_name: string
    actual_quota: number
    expected_quota: number
    difference_quota: number
  }>
}

type ClearCatalogReport = {
  mode: string
  provider_code: string
  base_url: string
  managed_tag_prefix: string
  channels_to_delete: number
  abilities_to_delete: number
  groups_to_delete: number
  model_pricing_items_to_delete: number
  special_pricing_to_delete: number
  task_billing_rules_to_delete: number
  tiered_billing_to_delete: number
  groups?: string[]
  models?: string[]
  changed_option_keys?: string[]
  applied: boolean
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
    ruleType: 'vector_bundle',
    baseUrl: '',
  },
  fiveTwoOne: {
    providerCode: '521',
    providerName: '521渠道',
    ruleType: '521_normal',
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
  { value: 'vector_bundle', label: '向量完整目录（普通 + 特殊）' },
  { value: 'vector_normal', label: '仅向量普通规则（不含固定价模型）' },
  { value: 'vector_special', label: '仅更新向量特殊规则（高级）' },
  { value: '521_normal', label: '521普通规则 JSON' },
  { value: 'shengge_normal', label: '胜哥普通规则 JSON' },
]

const APPLY_CONFIRM = 'APPLY_PROVIDER_CATALOG'
const CLEAR_CONFIRM = 'CLEAR_PROVIDER_CATALOG'

export function ProviderCatalogImportSection() {
  const [preset, setPreset] = useState<ProviderPreset>('vector')
  const [providerCode, setProviderCode] = useState(PRESETS.vector.providerCode)
  const [providerName, setProviderName] = useState(PRESETS.vector.providerName)
  const [ruleType, setRuleType] = useState(PRESETS.vector.ruleType)
  const [baseUrl, setBaseUrl] = useState('')
  const [ruleFile, setRuleFile] = useState<File | null>(null)
  const [normalRuleFile, setNormalRuleFile] = useState<File | null>(null)
  const [specialRuleFile, setSpecialRuleFile] = useState<File | null>(null)
  const [keyFile, setKeyFile] = useState<File | null>(null)
  const [groupConflictMode, setGroupConflictMode] = useState<'mark' | 'overwrite'>('mark')
  const [confirm, setConfirm] = useState('')
  const [report, setReport] = useState<ProviderCatalogReport | null>(null)
  const [message, setMessage] = useState('')
  const [loading, setLoading] = useState<'preview' | 'apply' | null>(null)
  const [auditLoading, setAuditLoading] = useState(false)
  const [auditReport, setAuditReport] = useState<BillingAuditReport | null>(
    null
  )
  const [clearConfirm, setClearConfirm] = useState('')
  const [clearLoading, setClearLoading] = useState<'preview' | 'apply' | null>(
    null
  )
  const [clearReport, setClearReport] = useState<ClearCatalogReport | null>(
    null
  )

  const clearDisabledReason = useMemo(() => {
    if (clearLoading !== null) return '清空请求处理中'
    return ''
  }, [clearLoading])

  const canSubmit = useMemo(
    () =>
      providerCode.trim() !== '' &&
      ruleType.trim() !== '' &&
      baseUrl.trim() !== '' &&
      (ruleType === 'vector_bundle'
        ? normalRuleFile !== null && specialRuleFile !== null
        : ruleFile !== null),
    [baseUrl, normalRuleFile, providerCode, ruleFile, ruleType, specialRuleFile]
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
    form.append('group_conflict_mode', groupConflictMode)
    if (ruleType === 'vector_bundle') {
      if (normalRuleFile) form.append('normal_rule_file', normalRuleFile)
      if (specialRuleFile) form.append('special_rule_file', specialRuleFile)
    } else if (ruleFile) {
      form.append('rule_file', ruleFile)
    }
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
      const blocked = (res.data.data.blocked_reasons?.length ?? 0) > 0
      setMessage(
        blocked
          ? 'dry-run 已完成，但存在阻断项，不能写入'
          : mode === 'apply'
            ? '已写入数据库'
            : 'dry-run 预览已生成'
      )
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '请求失败')
    } finally {
      setLoading(null)
    }
  }

  const runBillingAudit = async () => {
    setAuditLoading(true)
    setMessage('')
    try {
      const res = await api.get<ApiResponse<BillingAuditReport>>(
        '/api/provider-catalog/billing-audit?limit=10000'
      )
      if (!res.data.success) {
        setMessage(res.data.message || '历史计费扫描失败')
        return
      }
      setAuditReport(res.data.data)
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '历史计费扫描失败')
    } finally {
      setAuditLoading(false)
    }
  }

  const buildClearForm = (mode: 'preview' | 'apply') => {
    const form = new FormData()
    if (mode === 'apply') form.append('confirm', clearConfirm.trim())
    return form
  }

  const submitClear = async (mode: 'preview' | 'apply') => {
    setClearLoading(mode)
    setMessage('')
    try {
      const res = await api.post<ApiResponse<ClearCatalogReport>>(
        `/api/provider-catalog/clear/${mode}`,
        buildClearForm(mode)
      )
      if (!res.data.success) {
        setMessage(res.data.message || '清空失败')
        return
      }
      setClearReport(res.data.data)
      setMessage(mode === 'apply' ? '已清空供应商托管数据' : '清空预览已生成')
    } catch (error) {
      setMessage(error instanceof Error ? error.message : '清空请求失败')
    } finally {
      setClearLoading(null)
    }
  }

  return (
    <div className='grid gap-4'>
      <Card>
        <CardHeader>
          <CardTitle>供应商目录导入</CardTitle>
          <CardDescription>
            向量默认同时上传普通规则和特殊规则，特殊规则支持供应商导出的
            txt/JS 包或已清洗 JSON，统一 dry-run、统一确认并原子写入。
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
                <option value='fiveTwoOne'>521渠道</option>
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

          <div className='grid gap-3 md:grid-cols-3'>
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
            <Field label='重复分组处理'>
              <select
                className={selectClassName}
                value={groupConflictMode}
                onChange={(event) =>
                  setGroupConflictMode(
                    event.target.value as 'mark' | 'overwrite'
                  )
                }
              >
                <option value='mark'>新增供应商标记分组（推荐）</option>
                <option value='overwrite'>覆盖同名分组</option>
              </select>
              <div className='text-muted-foreground text-xs'>
                标记模式只在同名冲突时改为“供应商名:原分组名”；覆盖模式会沿用原分组名和倍率。
              </div>
            </Field>
          </div>

          <div className='grid gap-3 md:grid-cols-2'>
            {ruleType === 'vector_bundle' ? (
              <>
                <Field label='向量普通规则'>
                  <Input
                    type='file'
                    accept='.txt,.json'
                    onChange={(event) =>
                      setNormalRuleFile(event.target.files?.[0] ?? null)
                    }
                  />
                </Field>
                <Field label='向量特殊规则（txt / JS 包 / JSON）'>
                  <Input
                    type='file'
                    accept='.txt,.js,.json'
                    onChange={(event) =>
                      setSpecialRuleFile(event.target.files?.[0] ?? null)
                    }
                  />
                </Field>
              </>
            ) : (
              <Field label='规则文件'>
                <Input
                  type='file'
                  accept='.txt,.json'
                  onChange={(event) =>
                    setRuleFile(event.target.files?.[0] ?? null)
                  }
                />
              </Field>
            )}
            <Field label='分组 key 文件（可选）'>
              <Input
                type='file'
                accept='.xlsx'
                onChange={(event) =>
                  setKeyFile(event.target.files?.[0] ?? null)
                }
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
              !canSubmit ||
              confirm.trim() !== APPLY_CONFIRM ||
              loading !== null ||
              (report?.blocked_reasons?.length ?? 0) > 0
            }
            onClick={() => submit('apply')}
          >
            {loading === 'apply' ? '写入中...' : '确认写入数据库'}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>历史任务计费检查</CardTitle>
          <CardDescription>
            只生成受影响任务和差额报告，不会修改用户余额。
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          <Button
            type='button'
            variant='outline'
            disabled={auditLoading}
            onClick={runBillingAudit}
          >
            {auditLoading ? '扫描中...' : '运行 dry-run 扫描'}
          </Button>
          {auditReport && (
            <div className='grid gap-2 text-sm'>
              <div>
                扫描 {auditReport.scanned} 条，发现 {auditReport.affected}{' '}
                条异常，额度差额 {auditReport.difference_quota}
              </div>
              {auditReport.rows.slice(0, 100).map((row) => (
                <div
                  key={row.task_id}
                  className='grid gap-1 rounded-lg border p-3 md:grid-cols-4'
                >
                  <span>{row.model_name}</span>
                  <span className='break-all'>{row.task_id}</span>
                  <span>用户 #{row.user_id}</span>
                  <span>
                    实扣 {row.actual_quota} / 应扣 {row.expected_quota} / 差额{' '}
                    {row.difference_quota}
                  </span>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card className='border-destructive/40'>
        <CardHeader>
          <CardTitle>危险操作：清空供应商托管数据</CardTitle>
          <CardDescription>
            直接清空全部由供应商导入托管的渠道、abilities、模型计费配置和带供应商标记的分组；不删除用户、余额、支付、令牌和手工渠道。
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-3'>
          <div className='text-muted-foreground rounded-lg border border-destructive/20 bg-destructive/5 p-3 text-sm'>
            清空范围为所有 tag 以 catalog: 开头的供应商托管数据。建议先点 dry-run
            预览确认数量，确认后再输入确认文本执行。
          </div>
          {clearDisabledReason && (
            <div className='text-muted-foreground text-sm'>
              按钮不可用原因：{clearDisabledReason}
            </div>
          )}
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={clearDisabledReason !== ''}
              onClick={() => submitClear('preview')}
            >
              {clearLoading === 'preview' ? '预览中...' : '清空 dry-run 预览'}
            </Button>
          </div>
          {clearReport && <ClearReportView report={clearReport} />}
          <Field label={`输入 ${CLEAR_CONFIRM} 后允许清空`}>
            <Textarea
              className='min-h-10'
              value={clearConfirm}
              onChange={(event) => setClearConfirm(event.target.value)}
              placeholder={CLEAR_CONFIRM}
            />
          </Field>
          <Button
            type='button'
            variant='destructive'
            disabled={
              clearDisabledReason !== '' ||
              clearConfirm.trim() !== CLEAR_CONFIRM
            }
            onClick={() => submitClear('apply')}
          >
            {clearLoading === 'apply' ? '清空中...' : '确认清空供应商托管数据'}
          </Button>
        </CardContent>
      </Card>

      {message && (
        <div
          className={cn(
            'flex items-center gap-2 rounded-lg border px-3 py-2 text-sm',
            report && (report.blocked_reasons?.length ?? 0) === 0
              ? 'border-green-500/30 text-green-700'
              : 'border-red-500/30 text-red-700'
          )}
        >
          {report && (report.blocked_reasons?.length ?? 0) === 0 ? (
            <CheckCircle2 className='size-4' />
          ) : (
            <AlertCircle className='size-4' />
          )}
          {message}
        </div>
      )}

      {report && <ImportReportView report={report} />}
    </div>
  )
}

function ClearReportView({ report }: { report: ClearCatalogReport }) {
  const scope =
    report.provider_code || report.base_url
      ? `${report.provider_code || '*'} / ${report.base_url || '*'}`
      : '全部供应商托管数据'
  const items = [
    ['范围', scope],
    ['将删除渠道', report.channels_to_delete],
    ['将删除 abilities', report.abilities_to_delete],
    ['将删除分组', report.groups_to_delete],
    ['模型计费项', report.model_pricing_items_to_delete],
    ['特殊计费', report.special_pricing_to_delete],
    ['任务计费规则', report.task_billing_rules_to_delete],
    ['阶梯计费', report.tiered_billing_to_delete],
  ]
  return (
    <div className='grid gap-3 rounded-lg border p-3'>
      <div className='grid gap-2 md:grid-cols-4'>
        {items.map(([label, value]) => (
          <div key={label} className='rounded-lg border p-2'>
            <div className='text-muted-foreground text-xs'>{label}</div>
            <div className='mt-1 text-sm font-medium break-all'>{value}</div>
          </div>
        ))}
      </div>
      <ReportList title='将删除分组' values={report.groups} />
      <ReportList
        title='将更新的 Option'
        values={report.changed_option_keys}
      />
      <ReportList title='涉及模型' values={report.models?.slice(0, 100)} />
    </div>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
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
    ['任务计费规则', report.task_billing_rules],
    ['阶梯计费模型', report.tiered_billing_models],
    ['远端校验模型', report.remote_pricing_report?.checked_models ?? 0],
    [
      '重复分组处理',
      report.group_conflict_mode === 'overwrite'
        ? '覆盖同名分组'
        : '新增供应商标记分组',
    ],
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
              <div className='mt-1 text-sm font-medium break-all'>{value}</div>
            </div>
          ))}
        </div>

        <ReportList title='阻断原因' values={report.blocked_reasons} />
        <ReportList
          title='缺少 key 的分组'
          values={report.missing_key_groups}
        />
        <ReportList
          title='会更新的 Option'
          values={report.changed_option_keys}
        />
        <ReportList
          title='分组重命名'
          values={
            report.group_name_mappings
              ? Object.entries(report.group_name_mappings).map(
                  ([from, to]) => `${from} → ${to}`
                )
              : []
          }
        />
        <ReportList
          title='源文件 SHA-256'
          values={
            report.source_hashes
              ? Object.entries(report.source_hashes).map(
                  ([name, hash]) => `${name}: ${hash}`
                )
              : []
          }
        />
        {report.previous_source_hashes &&
          Object.keys(report.previous_source_hashes).length > 0 && (
            <div className='rounded-lg border p-3 text-sm'>
              与上次导入版本：
              {report.source_hashes_changed ? '文件内容有变化' : '文件内容相同'}
            </div>
          )}
        <ReportList
          title='跳过的特殊规则模型'
          values={report.skipped_special_models}
        />
        {report.remote_pricing_report?.mismatches &&
          report.remote_pricing_report.mismatches.length > 0 && (
            <div className='grid gap-2'>
              <div className='font-medium'>
                远端价格差异（
                {report.remote_pricing_report.mismatches.length}）
              </div>
              <div className='max-h-96 overflow-auto rounded-lg border'>
                {report.remote_pricing_report.mismatches.map((item, index) => (
                  <div
                    key={`${item.model_name}-${item.group}-${item.field}-${index}`}
                    className='grid gap-1 border-b p-3 text-sm last:border-b-0 md:grid-cols-5'
                  >
                    <span className='font-medium break-all'>
                      {item.model_name || '-'}
                    </span>
                    <span>{item.group || '-'}</span>
                    <span>{item.field}</span>
                    <span className='break-all'>
                      本地: {formatReportValue(item.local)}
                    </span>
                    <span className='break-all'>
                      远端: {formatReportValue(item.remote)}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        <ReportList
          title='远端额外模型（不阻断）'
          values={report.remote_pricing_report?.remote_only_models}
        />

        {report.invalid_rows && report.invalid_rows.length > 0 && (
          <div className='grid gap-2'>
            <div className='font-medium'>分组 key 文件错误</div>
            <div className='grid gap-2'>
              {report.invalid_rows.map((row) => (
                <div
                  key={`${row.row}-${row.field}`}
                  className='rounded-lg border p-2 text-sm'
                >
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

function formatReportValue(value: unknown) {
  if (value === undefined || value === null) return '-'
  if (typeof value === 'string') return value
  return JSON.stringify(value)
}

function ReportList({ title, values }: { title: string; values?: string[] }) {
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
