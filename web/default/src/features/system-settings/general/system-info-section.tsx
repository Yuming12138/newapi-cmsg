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
import type { Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, RefreshCw, RotateCcw, ServerCog } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatTimestampRelative, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ErrorState } from '@/components/error-state'
import { listSystemInstances } from '../api'
import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'
import type { SystemInstance, SystemInstanceStatus } from '../types'

const _systemInfoSchema = z.object({
  theme: z.object({
    frontend: z.enum(['default', 'classic']),
  }),
  SystemName: z.string().min(1),
  ServerAddress: z.string().optional(),
  Logo: z.string().url().optional().or(z.literal('')),
  Footer: z.string().optional(),
  About: z.string().optional(),
  HomePageContent: z.string().optional(),
  legal: z.object({
    user_agreement: z.string().optional(),
    privacy_policy: z.string().optional(),
  }),
})

type SystemInfoFormValues = z.infer<typeof _systemInfoSchema>

type SystemInfoSectionProps = {
  defaultValues: SystemInfoFormValues
}

const INSTANCE_POLL_INTERVAL_MS = 30_000

const STATUS_CLASS_NAME: Record<SystemInstanceStatus, string> = {
  online:
    'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  stale: 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
}

const STATUS_DOT_CLASS_NAME: Record<SystemInstanceStatus, string> = {
  online: 'bg-emerald-500',
  stale: 'bg-amber-500',
}

function roleLabel(instance: SystemInstance) {
  if (instance.info?.role?.is_master) return 'master'
  return 'worker'
}

function runtimeLabel(instance: SystemInstance) {
  const runtime = instance.info?.runtime
  if (!runtime?.goos && !runtime?.goarch) return '-'
  return [runtime.goos, runtime.goarch].filter(Boolean).join('/')
}

function getNodeName(instance: SystemInstance) {
  return instance.info?.node?.name || instance.node_name
}

function formatPercent(value?: number) {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return `${new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
  }).format(value)}%`
}

function formatBytes(bytes?: number): string {
  if (typeof bytes !== 'number' || Number.isNaN(bytes)) return '-'
  if (bytes === 0) return '0 B'
  if (bytes < 0) return `-${formatBytes(-bytes)}`

  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1
  )
  const value = bytes / 1024 ** index
  return `${new Intl.NumberFormat(undefined, {
    maximumFractionDigits: index === 0 ? 0 : 1,
  }).format(value)} ${units[index]}`
}

function SystemInstancesPanel() {
  const { t, i18n } = useTranslation()
  const instancesQuery = useQuery({
    queryKey: ['system-settings', 'system-info', 'instances'],
    queryFn: async () => {
      const res = await listSystemInstances()
      if (!res.success || !Array.isArray(res.data)) {
        throw new Error(res.message || t('We could not load system instances.'))
      }
      return res.data
    },
    staleTime: 30 * 1000,
    retry: false,
    refetchInterval: INSTANCE_POLL_INTERVAL_MS,
  })

  const instances = instancesQuery.data ?? []
  const refreshing = instancesQuery.isFetching && !instancesQuery.isLoading

  return (
    <SettingsSection
      title={t('System Instances')}
      description={t('Runtime nodes currently reporting into this deployment.')}
    >
      <div className='space-y-3'>
        <div className='flex items-center justify-between gap-3'>
          <span
            className='text-muted-foreground inline-flex items-center gap-1.5 text-xs'
            aria-live='polite'
          >
            <span className='size-1.5 rounded-full bg-emerald-500' />
            {t('Auto-refreshing every {{seconds}}s', {
              seconds: INSTANCE_POLL_INTERVAL_MS / 1000,
            })}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => instancesQuery.refetch()}
            disabled={refreshing}
          >
            <RefreshCw
              className={cn('mr-2 size-3.5', refreshing && 'animate-spin')}
            />
            {t('Refresh')}
          </Button>
        </div>

        {instancesQuery.isLoading ? (
          <div className='space-y-2 rounded-md border p-3'>
            <Skeleton className='h-8 w-full' />
            <Skeleton className='h-8 w-full' />
            <Skeleton className='h-8 w-3/4' />
          </div>
        ) : instancesQuery.isError ? (
          <ErrorState
            icon={AlertTriangle}
            title={t('Failed to load system instances')}
            description={
              instancesQuery.error instanceof Error
                ? instancesQuery.error.message
                : undefined
            }
            onRetry={() => instancesQuery.refetch()}
            className='min-h-[220px] rounded-md border'
          />
        ) : instances.length === 0 ? (
          <div className='text-muted-foreground rounded-md border p-4 text-sm'>
            {t('No system instances have reported yet.')}
          </div>
        ) : (
          <div className='overflow-x-auto rounded-md border'>
            <Table className='min-w-[980px]'>
              <TableHeader>
                <TableRow className='bg-muted/40 hover:bg-muted/40'>
                  <TableHead className='h-9 min-w-[220px] px-4 text-xs'>
                    {t('Instance')}
                  </TableHead>
                  <TableHead className='h-9 w-[110px] text-xs'>
                    {t('Status')}
                  </TableHead>
                  <TableHead className='h-9 w-[90px] text-xs'>
                    {t('Role')}
                  </TableHead>
                  <TableHead className='h-9 w-[90px] text-xs'>
                    {t('CPU')}
                  </TableHead>
                  <TableHead className='h-9 w-[90px] text-xs'>
                    {t('Memory')}
                  </TableHead>
                  <TableHead className='h-9 w-[140px] text-xs'>
                    {t('Storage')}
                  </TableHead>
                  <TableHead className='h-9 w-[110px] text-xs'>
                    {t('Version')}
                  </TableHead>
                  <TableHead className='h-9 w-[130px] text-xs'>
                    {t('Runtime')}
                  </TableHead>
                  <TableHead className='h-9 w-[165px] pr-4 text-xs'>
                    {t('Last Seen')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {instances.map((instance) => {
                  const storage = instance.info?.resources?.storage
                  return (
                    <TableRow key={instance.node_name}>
                      <TableCell className='px-4 py-2.5 align-middle'>
                        <div className='flex min-w-0 items-center gap-2'>
                          <ServerCog className='text-muted-foreground size-4 shrink-0' />
                          <div className='min-w-0'>
                            <div className='truncate font-medium'>
                              {getNodeName(instance)}
                            </div>
                            <div className='text-muted-foreground truncate text-xs'>
                              {instance.info?.host?.hostname || '-'}
                            </div>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell className='py-2.5 align-middle'>
                        <Badge
                          variant='secondary'
                          className={cn(
                            'gap-1.5',
                            STATUS_CLASS_NAME[instance.status]
                          )}
                        >
                          <span
                            className={cn(
                              'size-1.5 rounded-full',
                              STATUS_DOT_CLASS_NAME[instance.status]
                            )}
                          />
                          {t(instance.status)}
                        </Badge>
                      </TableCell>
                      <TableCell className='py-2.5 align-middle text-sm'>
                        {t(roleLabel(instance))}
                      </TableCell>
                      <TableCell className='py-2.5 align-middle font-mono text-xs'>
                        {formatPercent(
                          instance.info?.resources?.cpu?.usage_percent
                        )}
                      </TableCell>
                      <TableCell className='py-2.5 align-middle font-mono text-xs'>
                        {formatPercent(
                          instance.info?.resources?.memory?.usage_percent
                        )}
                      </TableCell>
                      <TableCell className='py-2.5 align-middle font-mono text-xs'>
                        <div>{formatPercent(storage?.used_percent)}</div>
                        <div className='text-muted-foreground'>
                          {formatBytes(storage?.used_bytes)} /{' '}
                          {formatBytes(storage?.total_bytes)}
                        </div>
                      </TableCell>
                      <TableCell className='py-2.5 align-middle font-mono text-xs'>
                        {instance.info?.runtime?.version || '-'}
                      </TableCell>
                      <TableCell className='py-2.5 align-middle font-mono text-xs'>
                        {runtimeLabel(instance)}
                      </TableCell>
                      <TableCell
                        className='text-muted-foreground py-2.5 pr-4 align-middle text-xs whitespace-nowrap'
                        title={formatTimestampToDate(instance.last_seen_at)}
                      >
                        {formatTimestampRelative(
                          instance.last_seen_at,
                          'seconds',
                          i18n.language
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </SettingsSection>
  )
}

function normalizeValue(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : String(value)
}

export function SystemInfoSection({ defaultValues }: SystemInfoSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const normalizedDefaults: SystemInfoFormValues = {
    theme: {
      frontend:
        defaultValues.theme?.frontend === 'classic' ? 'classic' : 'default',
    },
    SystemName: normalizeValue(defaultValues.SystemName),
    ServerAddress: normalizeValue(defaultValues.ServerAddress),
    Logo: normalizeValue(defaultValues.Logo),
    Footer: normalizeValue(defaultValues.Footer),
    About: normalizeValue(defaultValues.About),
    HomePageContent: normalizeValue(defaultValues.HomePageContent),
    legal: {
      user_agreement: normalizeValue(defaultValues.legal?.user_agreement),
      privacy_policy: normalizeValue(defaultValues.legal?.privacy_policy),
    },
  }

  const systemInfoSchemaWithI18n = z.object({
    theme: z.object({
      frontend: z.enum(['default', 'classic']),
    }),
    SystemName: z.string().min(1, {
      error: () => t('System name is required'),
    }),
    ServerAddress: z.string().optional(),
    Logo: z.string().url().optional().or(z.literal('')),
    Footer: z.string().optional(),
    About: z.string().optional(),
    HomePageContent: z.string().optional(),
    legal: z.object({
      user_agreement: z.string().optional(),
      privacy_policy: z.string().optional(),
    }),
  })

  const { form, handleSubmit, handleReset, isDirty, isSubmitting } =
    useSettingsForm<SystemInfoFormValues>({
      resolver: zodResolver(systemInfoSchemaWithI18n) as Resolver<
        SystemInfoFormValues,
        unknown,
        SystemInfoFormValues
      >,
      defaultValues: normalizedDefaults,
      onSubmit: async (_data, changedFields) => {
        // 主题切换会改变后端返回的前端产物，需放到最后处理：先更新其余设置项，
        // 仅当它们全部成功后才提交主题切换，避免其它设置失败时就切换了主题，
        // 导致用户停留或刷新到另一套前端不存在的路由而 404。
        const entries = Object.entries(changedFields)
        const themeEntry = entries.find(([key]) => key === 'theme.frontend')
        const otherEntries = entries.filter(([key]) => key !== 'theme.frontend')

        let allSucceeded = true
        for (const [key, value] of otherEntries) {
          let v = normalizeValue(value)
          if (key === 'ServerAddress') {
            v = v.replace(/\/+$/, '')
          }
          const res = await updateOption.mutateAsync({
            key,
            value: v,
          })
          if (!res.success) {
            allSucceeded = false
          }
        }
        if (themeEntry && !allSucceeded) {
          // Theme was not submitted; keep form state consistent with backend.
          _data.theme.frontend = normalizedDefaults.theme.frontend
          return
        }
        if (themeEntry && allSucceeded) {
          const res = await updateOption.mutateAsync({
            key: themeEntry[0],
            value: normalizeValue(themeEntry[1]),
          })
          if (res.success) {
            // 当前路由在另一套前端中并不存在，主题切换成功后重置到首页以避免 404。
            // 延时用于让表单脏状态先清除（移除 beforeunload 拦截）并展示成功提示后再刷新；
            // 使用 replace 让已失效的路由不进入历史，防止返回按钮再次触发 404。
            setTimeout(() => {
              window.location.replace('/')
            }, 600)
          } else {
            // Theme update failed; revert to the last saved value.
            _data.theme.frontend = normalizedDefaults.theme.frontend
          }
        }
      },
    })

  return (
    <>
      <FormNavigationGuard when={isDirty} />

      <SystemInstancesPanel />

      <SettingsSection
        title={t('System Information')}
        description={t('Configure basic system information and branding')}
      >
        <Form {...form}>
          <form onSubmit={handleSubmit} className='space-y-6'>
            <FormDirtyIndicator isDirty={isDirty} />
            <FormField
              control={form.control}
              name='theme.frontend'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Frontend Theme')}</FormLabel>
                  <Select
                    items={[
                      { value: 'default', label: t('Default (New Frontend)') },
                      {
                        value: 'classic',
                        label: t('Classic (Legacy Frontend)'),
                      },
                    ]}
                    onValueChange={field.onChange}
                    value={field.value}
                  >
                    <FormControl>
                      <SelectTrigger className='w-full'>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='default'>
                          {t('Default (New Frontend)')}
                        </SelectItem>
                        <SelectItem value='classic'>
                          {t('Classic (Legacy Frontend)')}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Switch between the new frontend and the classic frontend. Changes take effect after page reload.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='SystemName'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('System Name')}</FormLabel>
                  <FormControl>
                    <Input placeholder={t('New API')} {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('The name displayed across the application')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='ServerAddress'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Server Address')}</FormLabel>
                  <FormControl>
                    <Input placeholder='https://yourdomain.com' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'The public URL of your server, used for OAuth callbacks, webhooks, and other external integrations'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='Logo'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Logo URL')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('https://example.com/logo.png')}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('URL to your logo image (optional)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='Footer'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Footer')}</FormLabel>
                  <FormControl>
                    <Textarea
                      placeholder={t(
                        '© 2025 Your Company. All rights reserved.'
                      )}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Footer text displayed at the bottom of pages')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='About'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('About')}</FormLabel>
                  <FormControl>
                    <Textarea
                      placeholder={t(
                        'Enter HTML code (e.g., <p>About us...</p>) or a URL (e.g., https://example.com) to embed as iframe'
                      )}
                      rows={4}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Supports HTML markup or iframe embedding. Enter HTML code directly, or provide a complete URL to automatically embed it as an iframe.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='HomePageContent'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Home Page Content')}</FormLabel>
                  <FormControl>
                    <Textarea
                      placeholder={t('Welcome to our New API...')}
                      rows={6}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Content displayed on the home page (supports Markdown)'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='legal.user_agreement'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('User Agreement')}</FormLabel>
                  <FormControl>
                    <Textarea
                      placeholder={t(
                        'Provide Markdown, HTML, or an external URL for the user agreement'
                      )}
                      rows={6}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Leave empty to disable the agreement requirement. Supports Markdown, HTML, or a full URL to redirect users.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='legal.privacy_policy'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Privacy Policy')}</FormLabel>
                  <FormControl>
                    <Textarea
                      placeholder={t(
                        'Provide Markdown, HTML, or an external URL for the privacy policy'
                      )}
                      rows={6}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Leave empty to disable the privacy policy requirement. Supports Markdown, HTML, or a full URL to redirect users.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <div className='flex gap-2'>
              <Button
                type='submit'
                disabled={isSubmitting || updateOption.isPending}
              >
                {updateOption.isPending ? t('Saving...') : t('Save Changes')}
              </Button>
              <Button
                type='button'
                variant='outline'
                onClick={handleReset}
                disabled={!isDirty || updateOption.isPending || isSubmitting}
              >
                <RotateCcw className='mr-2 h-4 w-4' />
                {t('Reset')}
              </Button>
            </div>
          </form>
        </Form>
      </SettingsSection>
    </>
  )
}
