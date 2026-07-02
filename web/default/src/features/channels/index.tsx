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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { SectionPageLayout } from '@/components/layout'
import { getChannelOps } from './api'
import { ChannelsDialogs } from './components/channels-dialogs'
import { ChannelsPrimaryButtons } from './components/channels-primary-buttons'
import { ChannelsProvider } from './components/channels-provider'
import { ChannelsTable } from './components/channels-table'

export function Channels() {
  const { t } = useTranslation()
  const channelOpsQuery = useQuery({
    queryKey: ['channel-ops'],
    queryFn: getChannelOps,
    retry: false,
    staleTime: 5 * 60 * 1000,
  })
  const retryTimes = channelOpsQuery.data?.data?.retry_times
  const retryLabel =
    typeof retryTimes === 'number' ? `${t('Max Retries')}: ${retryTimes}` : null

  return (
    <ChannelsProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          <span className='flex min-w-0 items-center gap-2'>
            <span className='truncate'>{t('Channels')}</span>
            {retryLabel && (
              <Badge variant='outline' className='shrink-0 text-xs font-normal'>
                {retryLabel}
              </Badge>
            )}
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Description>
          {t('Manage API channels and provider configurations')}
        </SectionPageLayout.Description>
        <SectionPageLayout.Actions>
          <ChannelsPrimaryButtons />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <ChannelsTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ChannelsDialogs />
    </ChannelsProvider>
  )
}
