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
import * as z from 'zod'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
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
import { Switch } from '@/components/ui/switch'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'

const createPublicUrlCacheSchema = (t: (key: string) => string) =>
  z.object({
    PublicUrlCacheEnabled: z.boolean(),
    PublicUrlCacheProvider: z.literal('local'),
    PublicUrlCachePrefix: z.string().min(1, t('Prefix is required')),
    PublicUrlCacheTTLSeconds: z.number().int().positive(),
    PublicUrlCacheMaxImageMB: z.number().int().positive(),
    PublicUrlCacheLocalPath: z.string().min(1, t('Local path is required')),
    PublicUrlCachePublicBaseURL: z.string(),
  })

type PublicUrlCacheFormValues = z.infer<
  ReturnType<typeof createPublicUrlCacheSchema>
>

type PublicUrlCacheSettingsSectionProps = {
  defaultValues: PublicUrlCacheFormValues
}

export function PublicUrlCacheSettingsSection({
  defaultValues,
}: PublicUrlCacheSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const schema = createPublicUrlCacheSchema(t)

  const form = useForm<PublicUrlCacheFormValues>({
    resolver: zodResolver(schema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const onSubmit = async (values: PublicUrlCacheFormValues) => {
    const updates: Array<{ key: string; value: string | boolean | number }> = []

    for (const [key, value] of Object.entries(values)) {
      const typedKey = key as keyof PublicUrlCacheFormValues
      if (value === defaultValues[typedKey]) {
        continue
      }
      updates.push({ key, value })
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
  }

  const textFields: Array<{
    name: keyof PublicUrlCacheFormValues
    label: string
    description?: string
    type?: string
  }> = [
    {
      name: 'PublicUrlCacheProvider',
      label: t('Provider'),
      description: t('Local server storage is used.'),
    },
    {
      name: 'PublicUrlCachePrefix',
      label: t('Object prefix'),
      description: t(
        'Public URLs are served from /public-url-cache/{prefix}/...'
      ),
    },
    {
      name: 'PublicUrlCacheTTLSeconds',
      label: t('TTL seconds'),
      type: 'number',
    },
    {
      name: 'PublicUrlCacheMaxImageMB',
      label: t('Max image size MB'),
      type: 'number',
    },
    {
      name: 'PublicUrlCacheLocalPath',
      label: t('Local cache path'),
      description: t(
        'Relative paths are resolved from the new-api working directory.'
      ),
    },
    {
      name: 'PublicUrlCachePublicBaseURL',
      label: t('Public base URL'),
      description: t(
        'Optional. Empty uses ServerAddress. Must be reachable by upstream gateways.'
      ),
    },
  ]

  return (
    <SettingsSection title={t('Public URL Cache')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save public URL cache settings'
          />

          <FormField
            control={form.control}
            name='PublicUrlCacheEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable public URL cache')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Stores temporary ToonFlow reference images on this server and exposes public URLs for upstream gateways.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />

          <div className='grid gap-4 md:grid-cols-2'>
            {textFields.map((item) => (
              <FormField
                key={item.name}
                control={form.control}
                name={item.name}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{item.label}</FormLabel>
                    <FormControl>
                      <Input
                        type={item.type ?? 'text'}
                        autoComplete='off'
                        {...field}
                        value={String(field.value ?? '')}
                        onChange={(event) => {
                          if (item.type === 'number') {
                            field.onChange(Number(event.target.value))
                            return
                          }
                          field.onChange(event.target.value)
                        }}
                      />
                    </FormControl>
                    {item.description ? (
                      <FormDescription>{item.description}</FormDescription>
                    ) : null}
                    <FormMessage />
                  </FormItem>
                )}
              />
            ))}
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
