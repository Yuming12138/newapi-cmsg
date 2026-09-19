/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { testChannelCapability } from '../../api'
import { useChannels } from '../channels-provider'

type CapabilityTestDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type CapabilityTestResult = {
  success?: boolean
  quality_pass?: boolean
  status?: string
  failure_reason?: string
  requested_model?: string
  observed_model?: string
  answer_preview?: string
  reason?: string
  error?: string
  latency_ms?: number
}

const CAPABILITY_PROMPT =
  'Solve this problem carefully without external tools. A black bag contains candies with three flavors and two shapes. The counts are: apple round 7, apple star 7, peach round 9, peach star 6, watermelon round 8, watermelon star 4. What is the minimum number of candies to draw to guarantee having apple and peach candies of different shapes? End with exactly FINAL_ANSWER: <number> on its own line.'

const PREFERRED_MODELS = [
  'gpt-6-astra',
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna',
  'gpt-5.5',
]

function getErrorMessage(error: unknown) {
  if (error instanceof Error && error.message) return error.message
  return 'Capability test failed'
}

export function CapabilityTestDialog({
  open,
  onOpenChange,
}: CapabilityTestDialogProps) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const [capabilityModel, setCapabilityModel] = useState('')
  const [isCapabilityTesting, setIsCapabilityTesting] = useState(false)
  const [capabilityResult, setCapabilityResult] =
    useState<CapabilityTestResult | null>(null)
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const [testStartedAt, setTestStartedAt] = useState<number | null>(null)

  const capabilityModels = useMemo(
    () =>
      (currentRow?.models ?? '')
        .split(',')
        .map((model) => model.trim())
        .filter(Boolean),
    [currentRow?.models]
  )

  // Reset only when a new dialog session/channel is opened. A channel-list
  // refetch does not change this page-level component, so a long test keeps
  // its dialog, loading state, and result visible.
  useEffect(() => {
    if (!open || !currentRow) return
    const defaultModel =
      PREFERRED_MODELS.find((model) => capabilityModels.includes(model)) ??
      capabilityModels[0] ??
      ''
    setCapabilityModel(defaultModel)
    setCapabilityResult(null)
    setElapsedSeconds(0)
    setTestStartedAt(null)
  }, [open, currentRow?.id, capabilityModels])

  useEffect(() => {
    if (!isCapabilityTesting || testStartedAt == null) return
    const updateElapsed = () => {
      setElapsedSeconds(Math.max(0, Math.floor((Date.now() - testStartedAt) / 1000)))
    }
    updateElapsed()
    const timer = window.setInterval(updateElapsed, 1000)
    return () => window.clearInterval(timer)
  }, [isCapabilityTesting, testStartedAt])

  if (!currentRow) return null

  const runCapabilityTest = async () => {
    if (isCapabilityTesting || !capabilityModel) return
    const startedAt = Date.now()
    setIsCapabilityTesting(true)
    setTestStartedAt(startedAt)
    setElapsedSeconds(0)
    setCapabilityResult(null)
    try {
      const result = await testChannelCapability(
        currentRow.id,
        capabilityModel || undefined
      )
      setCapabilityResult(result as CapabilityTestResult)
      if (result.success) {
        (result.quality_pass ? toast.success : toast.error)(
          t('Capability test: {{status}} ({{latency}} ms)', {
            status: result.failure_reason || result.status,
            latency: result.latency_ms,
          })
        )
      } else {
        toast.error(result.reason || t('Capability test failed'))
      }
    } catch (error) {
      setCapabilityResult({
        success: false,
        status: 'error',
        requested_model: capabilityModel,
        reason: getErrorMessage(error),
      })
      toast.error(getErrorMessage(error))
    } finally {
      setIsCapabilityTesting(false)
      setTestStartedAt(null)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!isCapabilityTesting) onOpenChange(nextOpen)
      }}
    >
      <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Test Capability')}</DialogTitle>
          <DialogDescription>
            {t(
              'Select a text model to test on this channel. Image models are excluded from this gray test.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='rounded-md border bg-muted/30 p-3 text-sm'>
          <div className='mb-1 font-medium'>{t('Prompt')}</div>
          <pre className='max-h-40 overflow-auto whitespace-pre-wrap text-xs'>
            {CAPABILITY_PROMPT}
          </pre>
        </div>

        <Select
          value={capabilityModel}
          onValueChange={(value) => setCapabilityModel(value ?? '')}
          disabled={isCapabilityTesting}
        >
          <SelectTrigger>
            <SelectValue placeholder={t('Select a model')} />
          </SelectTrigger>
          <SelectContent>
            {capabilityModels.map((model) => (
              <SelectItem key={model} value={model}>
                {model}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        {isCapabilityTesting && (
          <div className='rounded-md border border-dashed p-3 text-sm text-muted-foreground'>
            {t('Waiting for the model to finish...')} ({elapsedSeconds}s)
          </div>
        )}

        {capabilityResult && (
          <div className='max-h-96 overflow-auto rounded-md border p-3 text-sm'>
            <div>
              <b>{t('Result')}:</b> {capabilityResult.status || t('unknown')}
            </div>
            <div>
              <b>{t('Failure reason')}:</b>{' '}
              {capabilityResult.failure_reason || t('none')}
            </div>
            <div>
              <b>{t('Requested model')}:</b>{' '}
              {capabilityResult.requested_model || capabilityModel}
            </div>
            <div>
              <b>{t('Observed model')}:</b>{' '}
              {capabilityResult.observed_model || t('unknown')}
            </div>
            <pre className='mt-2 whitespace-pre-wrap text-xs'>
              {capabilityResult.answer_preview ||
                capabilityResult.reason ||
                capabilityResult.error ||
                t('No response content')}
            </pre>
          </div>
        )}

        <DialogFooter>
          <Button
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={isCapabilityTesting}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={runCapabilityTest}
            disabled={isCapabilityTesting || !capabilityModel}
          >
            {isCapabilityTesting ? t('Testing') : t('Run test')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
