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
import { HtmlContent } from '@/components/html-content'
import { testChannelCapability } from '../../api'
import { useChannels } from '../channels-provider'

type CapabilityTestDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type CapabilityTestResult = {
  success?: boolean
  quality_pass?: boolean
  structure_pass?: boolean
  evaluation_scope?: string
  status?: string
  failure_reason?: string
  requested_model?: string
  observed_model?: string
  answer_preview?: string
  svg_detected?: boolean
  svg_preview?: string
  svg_metrics?: {
    view_box?: string
    paths?: number
    groups?: number
    gradients?: number
    animations?: number
    bytes?: number
  }
  reason?: string
  error?: string
  latency_ms?: number
  reasoning_effort?: string
}

const CAPABILITY_EFFORT_OPTIONS = [
  { value: 'auto', label: 'Auto / upstream default' },
  { value: 'none', label: 'None' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'xhigh', label: 'Extra high' },
  { value: 'max', label: 'Max' },
  { value: 'ultra', label: 'Ultra' },
]

const CAPABILITY_PROMPT = `Create a polished, richly detailed self-contained SVG illustration of a pelican riding a bicycle along a coastal road. Use layered gradients, soft shadows, highlights, detailed feathers, a long articulated beak, wings, and legs that connect naturally to the pedals. Draw a convincing bicycle with two large spoked wheels, a frame, chain, crank and handlebars. Add a coastal background with sky, clouds, sea, waves, road and foreground details. Use smooth looping SVG/CSS animations for wheel rotation, crank and pedal motion, leg pedaling, body bobbing and background motion. Use a wide 16:9 viewBox, a balanced composition and a polished color palette. Return only the complete <svg>...</svg> document with no Markdown fence and no explanation.`

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
  const { capabilityTestChannel: channel } = useChannels()
  const [capabilityModel, setCapabilityModel] = useState('')
  const [capabilityEffort, setCapabilityEffort] = useState('auto')
  const [isCapabilityTesting, setIsCapabilityTesting] = useState(false)
  const [capabilityResult, setCapabilityResult] =
    useState<CapabilityTestResult | null>(null)
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const [testStartedAt, setTestStartedAt] = useState<number | null>(null)

  const capabilityModels = useMemo(
    () =>
      (channel?.models ?? '')
        .split(',')
        .map((model) => model.trim())
        .filter(Boolean),
    [channel?.models]
  )

  // Reset only when a new dialog session/channel is opened. A channel-list
  // refetch does not change this page-level component, so a long test keeps
  // its dialog, loading state, and result visible.
  useEffect(() => {
    if (!open || !channel) return
    const defaultModel =
      PREFERRED_MODELS.find((model) => capabilityModels.includes(model)) ??
      capabilityModels[0] ??
      ''
    setCapabilityModel(defaultModel)
    setCapabilityResult(null)
    setCapabilityEffort('auto')
    setElapsedSeconds(0)
    setTestStartedAt(null)
  }, [open, channel?.id, capabilityModels])

  useEffect(() => {
    if (!isCapabilityTesting || testStartedAt == null) return
    const updateElapsed = () => {
      setElapsedSeconds(
        Math.max(0, Math.floor((Date.now() - testStartedAt) / 1000))
      )
    }
    updateElapsed()
    const timer = window.setInterval(updateElapsed, 1000)
    return () => window.clearInterval(timer)
  }, [isCapabilityTesting, testStartedAt])

  if (!channel) return null

  const runCapabilityTest = async () => {
    if (isCapabilityTesting || !capabilityModel) return
    const startedAt = Date.now()
    setIsCapabilityTesting(true)
    setTestStartedAt(startedAt)
    setElapsedSeconds(0)
    setCapabilityResult(null)
    try {
      const result = await testChannelCapability(
        channel.id,
        capabilityModel || undefined,
        capabilityEffort === 'auto' ? undefined : capabilityEffort
      )
      setCapabilityResult(result as CapabilityTestResult)
      if (result.success) {
        ;((result.structure_pass ?? result.quality_pass)
          ? toast.success
          : toast.error)(
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
        reasoning_effort: capabilityEffort,
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
              'Generate a detailed animated pelican SVG in one turn without tools. The preview is sanitized before display; this compares channel output, not Codex multi-turn editing.'
            )}
          </DialogDescription>
        </DialogHeader>

        <div className='bg-muted/30 rounded-md border p-3 text-sm'>
          <div className='mb-1 font-medium'>{t('Prompt')}</div>
          <pre className='max-h-40 overflow-auto text-xs whitespace-pre-wrap'>
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

        <Select
          value={capabilityEffort}
          onValueChange={(value) => setCapabilityEffort(value ?? 'auto')}
          disabled={isCapabilityTesting}
        >
          <SelectTrigger>
            <SelectValue placeholder={t('Select reasoning effort')} />
          </SelectTrigger>
          <SelectContent>
            {CAPABILITY_EFFORT_OPTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {t(option.label)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        {isCapabilityTesting && (
          <div className='text-muted-foreground rounded-md border border-dashed p-3 text-sm'>
            {t('Waiting for the model to finish...')} ({elapsedSeconds}s)
          </div>
        )}

        {capabilityResult && (
          <div className='max-h-[60vh] overflow-auto rounded-md border p-3 text-sm'>
            <div>
              <b>{t('Result')}:</b>{' '}
              {(capabilityResult.structure_pass ??
              capabilityResult.quality_pass)
                ? t('SVG structure valid')
                : capabilityResult.status || t('unknown')}
            </div>
            <div className='text-muted-foreground'>
              {t(
                'The test runs one turn without tools. It checks that the SVG is structurally valid and contains drawing elements; it does not score artistic quality.'
              )}
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
            {capabilityResult.svg_metrics && (
              <div className='text-muted-foreground mt-1 flex flex-wrap gap-x-4 gap-y-1'>
                <span>
                  {t('Canvas')}:{' '}
                  {capabilityResult.svg_metrics.view_box || t('unknown')}
                </span>
                <span>
                  {t('Drawing elements')}:{' '}
                  {capabilityResult.svg_metrics.paths ?? 0}
                </span>
                <span>
                  {t('Groups')}: {capabilityResult.svg_metrics.groups ?? 0}
                </span>
                <span>
                  {t('Gradients')}:{' '}
                  {capabilityResult.svg_metrics.gradients ?? 0}
                </span>
                <span>
                  {t('Animations')}:{' '}
                  {capabilityResult.svg_metrics.animations ?? 0}
                </span>
                <span>
                  {t('SVG size')}:{' '}
                  {Math.round((capabilityResult.svg_metrics.bytes ?? 0) / 1024)} KB
                </span>
              </div>
            )}
            {capabilityResult.svg_preview && (
              <div className='bg-muted/20 mt-3 rounded-md border p-2'>
                <div className='mb-2 font-medium'>{t('SVG preview')}</div>
                <div className='bg-background max-h-[28rem] min-h-48 overflow-auto rounded-sm p-2'>
                  <HtmlContent
                    content={capabilityResult.svg_preview}
                    variant='isolated'
                    className='min-h-44 w-full'
                  />
                </div>
              </div>
            )}
            <div className='mt-3'>
              <div className='mb-1 font-medium'>{t('Raw model response')}</div>
              <pre className='max-h-80 overflow-auto text-xs whitespace-pre-wrap'>
                {capabilityResult.answer_preview ||
                  capabilityResult.reason ||
                  capabilityResult.error ||
                  t('No response content')}
              </pre>
            </div>
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
