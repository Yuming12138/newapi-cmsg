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
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  StatusBadge,
  type StatusBadgeProps,
  type StatusVariant,
} from './status-badge'

type GroupBadgeProps = Omit<
  StatusBadgeProps,
  'autoColor' | 'label' | 'variant'
> & {
  group?: string | null
  label?: string
  ratio?: number | null
}

const groupVariantOverrides: Record<string, StatusVariant> = {
  // Match the green/teal visual used by the internal asxs group instead of
  // the default name hash, which maps cliproxy-codex to a misleading red.
  'cliproxy-codex': 'teal',
}

function getGroupRatioClassName(ratio: number): string {
  if (ratio > 1) {
    return 'border-warning/25 bg-warning/10 text-warning'
  }
  if (ratio < 1) {
    return 'border-info/25 bg-info/10 text-info'
  }
  return 'border-border bg-muted text-muted-foreground'
}

function getGroupLabel(params: {
  labelOverride?: string
  groupName?: string
  isAutoGroup: boolean
  isEmptyGroup: boolean
  t: (key: string) => string
}): string {
  if (params.labelOverride) return params.labelOverride
  if (params.isEmptyGroup) return params.t('User Group')
  if (params.isAutoGroup) return params.t('Auto')
  return params.groupName ?? ''
}

export function GroupBadge(props: GroupBadgeProps) {
  const { t } = useTranslation()
  const {
    group,
    label: labelOverride,
    ratio,
    copyable = false,
    showDot,
    className,
    ...badgeProps
  } = props
  const groupName = group?.trim()
  const isAutoGroup = groupName === 'auto'
  const isEmptyGroup = !groupName
  const isSpecialGroup = isAutoGroup || isEmptyGroup
  const groupVariant = groupName
    ? groupVariantOverrides[groupName.toLowerCase()]
    : undefined
  const label = getGroupLabel({
    labelOverride,
    groupName,
    isAutoGroup,
    isEmptyGroup,
    t,
  })

  const badge = (
    <StatusBadge
      {...badgeProps}
      copyable={copyable}
      label={label}
      showDot={showDot ?? (isSpecialGroup ? false : undefined)}
      variant={isSpecialGroup ? 'neutral' : groupVariant}
      autoColor={isSpecialGroup || groupVariant ? undefined : groupName}
      className={cn('min-w-0 shrink overflow-hidden', className)}
    />
  )

  if (ratio == null) {
    return badge
  }

  return (
    <span className='inline-flex max-w-full min-w-0 items-center gap-2 text-xs'>
      <span className='max-w-full min-w-0 overflow-hidden'>{badge}</span>
      <span
        className={cn(
          'inline-flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 font-mono text-[11px] leading-none tabular-nums',
          getGroupRatioClassName(ratio)
        )}
      >
        <span className='size-1 rounded-full bg-current opacity-60' />
        <span>{ratio}x</span>
      </span>
    </span>
  )
}
