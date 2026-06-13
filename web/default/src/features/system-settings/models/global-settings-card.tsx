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
import { useEffect } from 'react'
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { StatusBadge } from '@/components/status-badge'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const thinkingBlacklistExample = JSON.stringify(
  ['moonshotai/kimi-k2-thinking', 'kimi-k2-thinking'],
  null,
  2
)

const imageEditDataURLWhitelistExample = JSON.stringify([1, 7, 12], null, 2)

const imageEditDataURLBlacklistExample = JSON.stringify([23, 45], null, 2)

const chatToResponsesPolicyExample = JSON.stringify(
  {
    enabled: true,
    all_channels: false,
    channel_ids: [1, 2],
    model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
  },
  null,
  2
)

const chatToResponsesPolicyAllChannelsExample = JSON.stringify(
  {
    enabled: true,
    all_channels: true,
    model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
  },
  null,
  2
)

const jsonString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  try {
    JSON.parse(trimmed)
    return true
  } catch {
    return false
  }
}, 'Invalid JSON format')

const userIDListJsonString = z.string().refine((value) => {
  const trimmed = value.trim()
  if (!trimmed) return true
  try {
    const parsed = JSON.parse(trimmed)
    return (
      Array.isArray(parsed) &&
      parsed.every((userID) => Number.isInteger(userID) && userID > 0)
    )
  } catch {
    return false
  }
}, 'Must be a JSON array of positive integer user IDs')

const schema = z.object({
  global: z.object({
    pass_through_request_enabled: z.boolean(),
    thinking_model_blacklist: jsonString,
    image_edit_data_url_user_whitelist: userIDListJsonString,
    image_edit_data_url_user_blacklist: userIDListJsonString,
    chat_completions_to_responses_policy: jsonString,
  }),
  general_setting: z.object({
    ping_interval_enabled: z.boolean(),
    ping_interval_seconds: z.coerce.number().min(1),
  }),
})

type GlobalModelSettingsFormValues = z.output<typeof schema>
type GlobalModelSettingsFormInput = z.input<typeof schema>

type FlatGlobalModelSettings = {
  'global.pass_through_request_enabled': boolean
  'global.thinking_model_blacklist': string
  'global.image_edit_data_url_user_whitelist': string
  'global.image_edit_data_url_user_blacklist': string
  'global.chat_completions_to_responses_policy': string
  'general_setting.ping_interval_enabled': boolean
  'general_setting.ping_interval_seconds': number
}

const flattenGlobalValues = (
  values: GlobalModelSettingsFormValues
): FlatGlobalModelSettings => ({
  'global.pass_through_request_enabled':
    values.global.pass_through_request_enabled,
  'global.thinking_model_blacklist': normalizeJsonText(
    values.global.thinking_model_blacklist,
    '[]'
  ),
  'global.image_edit_data_url_user_whitelist': normalizeJsonText(
    values.global.image_edit_data_url_user_whitelist,
    '[]'
  ),
  'global.image_edit_data_url_user_blacklist': normalizeJsonText(
    values.global.image_edit_data_url_user_blacklist,
    '[]'
  ),
  'global.chat_completions_to_responses_policy': normalizeJsonText(
    values.global.chat_completions_to_responses_policy,
    '{}'
  ),
  'general_setting.ping_interval_enabled':
    values.general_setting.ping_interval_enabled,
  'general_setting.ping_interval_seconds':
    values.general_setting.ping_interval_seconds,
})

function normalizeJsonText(value: string, fallback: string) {
  const trimmed = (value ?? '').toString().trim()
  return trimmed ? trimmed : fallback
}

type GlobalSettingsCardProps = {
  defaultValues: GlobalModelSettingsFormValues
}

export function GlobalSettingsCard({ defaultValues }: GlobalSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<
    GlobalModelSettingsFormInput,
    unknown,
    GlobalModelSettingsFormValues
  >({
    resolver: zodResolver(schema),
    defaultValues: defaultValues as GlobalModelSettingsFormInput,
  })

  useEffect(() => {
    form.reset(defaultValues as GlobalModelSettingsFormInput)
  }, [defaultValues, form])

  const pingEnabled = form.watch('general_setting.ping_interval_enabled')

  const formatJsonField = (
    field:
      | 'global.thinking_model_blacklist'
      | 'global.image_edit_data_url_user_whitelist'
      | 'global.image_edit_data_url_user_blacklist'
      | 'global.chat_completions_to_responses_policy'
  ) => {
    const raw = form.getValues(field)
    if (!raw || !raw.trim()) return
    try {
      const formatted = JSON.stringify(JSON.parse(raw), null, 2)
      form.setValue(field, formatted, { shouldDirty: true })
    } catch {
      toast.error(t('Invalid JSON format'))
    }
  }

  const onSubmit = async (values: GlobalModelSettingsFormValues) => {
    const flattenedDefaults = flattenGlobalValues(defaultValues)
    const flattenedValues = flattenGlobalValues(values)
    const updates = Object.entries(flattenedValues).filter(
      ([key, value]) =>
        value !== flattenedDefaults[key as keyof FlatGlobalModelSettings]
    )

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const [key, value] of updates) {
      await updateOption.mutateAsync({
        key,
        value,
      })
    }
  }

  return (
    <SettingsSection title={t('Global Model Configuration')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />
          <FormField
            control={form.control}
            name='global.pass_through_request_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Request Passthrough')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Forward requests directly to upstream providers without any post-processing.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='global.thinking_model_blacklist'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Models that skip thinking suffix processing')}
                </FormLabel>
                <FormControl>
                  <Textarea
                    rows={4}
                    placeholder={`${t('Example:')}\n${thinkingBlacklistExample}`}
                    {...field}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Models listed here will not automatically append or remove -thinking / -nothinking suffixes.'
                  )}
                </FormDescription>
                <div className='flex flex-wrap gap-2'>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() =>
                      formatJsonField('global.thinking_model_blacklist')
                    }
                  >
                    {t('Format JSON')}
                  </Button>
                </div>
                <FormMessage />
              </FormItem>
            )}
          />

          <Separator />

          <div className='space-y-4'>
            <div>
              <h3 className='text-base font-semibold'>
                {t('Reference image Data URL conversion')}
              </h3>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Applies only to JSON reference image requests that require server-side multipart or file upload conversion. Existing multipart requests are not affected.'
                )}
              </p>
            </div>

            <FormField
              control={form.control}
              name='global.image_edit_data_url_user_whitelist'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Conversion user ID whitelist')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={4}
                      placeholder={`${t('Example:')}\n${imageEditDataURLWhitelistExample}`}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Empty means all authenticated users are allowed.')}
                  </FormDescription>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() =>
                      formatJsonField(
                        'global.image_edit_data_url_user_whitelist'
                      )
                    }
                  >
                    {t('Format JSON')}
                  </Button>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='global.image_edit_data_url_user_blacklist'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Conversion user ID blacklist')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={4}
                      placeholder={`${t('Example:')}\n${imageEditDataURLBlacklistExample}`}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Blacklist takes precedence over whitelist.')}
                  </FormDescription>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() =>
                      formatJsonField(
                        'global.image_edit_data_url_user_blacklist'
                      )
                    }
                  >
                    {t('Format JSON')}
                  </Button>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <div className='space-y-4'>
            <div className='flex items-center gap-2'>
              <h3 className='text-base font-semibold'>
                {t('ChatCompletions -> Responses Compatibility')}
              </h3>
              <StatusBadge
                label={t('Preview')}
                variant='neutral'
                copyable={false}
              />
            </div>

            <Alert>
              <AlertTitle>{t('Warning')}</AlertTitle>
              <AlertDescription>
                {t(
                  'This feature is experimental. Configuration format and behavior may change.'
                )}
              </AlertDescription>
            </Alert>

            <FormField
              control={form.control}
              name='global.chat_completions_to_responses_policy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Policy JSON')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={8}
                      placeholder={`${t('Example (specific channels):')}\n${chatToResponsesPolicyExample}\n\n${t('Example (all channels):')}\n${chatToResponsesPolicyAllChannelsExample}`}
                      {...field}
                      onChange={(event) => field.onChange(event.target.value)}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Empty value will be saved as {}.')}
                  </FormDescription>
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        form.setValue(
                          'global.chat_completions_to_responses_policy',
                          chatToResponsesPolicyExample,
                          { shouldDirty: true }
                        )
                      }
                    >
                      {t('Fill example (specific channels)')}
                    </Button>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        form.setValue(
                          'global.chat_completions_to_responses_policy',
                          chatToResponsesPolicyAllChannelsExample,
                          { shouldDirty: true }
                        )
                      }
                    >
                      {t('Fill example (all channels)')}
                    </Button>
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() =>
                        formatJsonField(
                          'global.chat_completions_to_responses_policy'
                        )
                      }
                    >
                      {t('Format JSON')}
                    </Button>
                  </div>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <Separator />

          <FormField
            control={form.control}
            name='general_setting.ping_interval_enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Keep-alive Ping')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Periodically send ping frames to keep streaming connections active.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='general_setting.ping_interval_seconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Ping Interval (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    disabled={!pingEnabled}
                    className='w-24'
                    value={
                      field.value === undefined || field.value === null
                        ? ''
                        : String(field.value)
                    }
                    onChange={(event) => field.onChange(event.target.value)}
                    onBlur={field.onBlur}
                    name={field.name}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Recommended to keep this high to avoid upstream throttling.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
