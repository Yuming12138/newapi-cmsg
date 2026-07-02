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
import { useCallback, useEffect, useRef, useState } from 'react'
import type { OnChangeFn, VisibilityState } from '@tanstack/react-table'

function readStoredColumnVisibility(storageKey: string): VisibilityState {
  if (typeof window === 'undefined') return {}

  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) return {}

    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }

    return Object.entries(parsed).reduce<VisibilityState>(
      (visibility, [key, value]) => {
        if (typeof value === 'boolean') {
          visibility[key] = value
        }
        return visibility
      },
      {}
    )
  } catch {
    return {}
  }
}

function persistColumnVisibility(
  storageKey: string,
  columnVisibility: VisibilityState
) {
  if (typeof window === 'undefined') return

  try {
    window.localStorage.setItem(storageKey, JSON.stringify(columnVisibility))
  } catch {
    // Storage can be unavailable in private mode; table controls still work.
  }
}

export function useColumnVisibilityStorage(
  storageKey: string,
  initialColumnVisibility: VisibilityState = {}
) {
  const initialColumnVisibilityRef = useRef(initialColumnVisibility)
  const [hydratedStorageKey, setHydratedStorageKey] = useState(storageKey)
  const [columnVisibility, setColumnVisibilityState] =
    useState<VisibilityState>(() => ({
      ...initialColumnVisibility,
      ...readStoredColumnVisibility(storageKey),
    }))

  useEffect(() => {
    if (hydratedStorageKey === storageKey) return

    setHydratedStorageKey(storageKey)
    setColumnVisibilityState({
      ...initialColumnVisibilityRef.current,
      ...readStoredColumnVisibility(storageKey),
    })
  }, [hydratedStorageKey, storageKey])

  const setColumnVisibility = useCallback<OnChangeFn<VisibilityState>>(
    (updater) => {
      setColumnVisibilityState((previous) => {
        const next =
          typeof updater === 'function' ? updater(previous) : updater
        persistColumnVisibility(storageKey, next)
        return next
      })
    },
    [storageKey]
  )

  return [columnVisibility, setColumnVisibility] as const
}
