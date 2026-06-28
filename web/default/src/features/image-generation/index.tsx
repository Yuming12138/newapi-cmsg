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
import {
  type ChangeEvent,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from 'react'
import axios from 'axios'
import { useQuery } from '@tanstack/react-query'
import {
  CheckCircle2,
  Clock3,
  Download,
  ImageIcon,
  Images,
  Loader2,
  RotateCcw,
  Trash2,
  WandSparkles,
  XCircle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { SectionPageLayout } from '@/components/layout'
import { getUserGroups } from '@/features/playground/api'
import {
  cancelImageTask,
  editImage,
  generateImage,
  getImageTask,
  listImageTasks,
} from './api'
import type {
  GeneratedImage,
  ImageMode,
  ImageGenerationTask,
  ImageOutputFormat,
  ImageQuality,
  ImageSize,
} from './types'

const MODEL = 'gpt-image-2' as const
const PREFERRED_GROUP = 'cliproxy-codex'

const SIZES: ImageSize[] = ['1024x1024', '1536x1024', '1024x1536', 'auto']
const QUALITIES: ImageQuality[] = ['auto', 'high', 'medium', 'low']
const QUALITY_LABELS: Record<ImageQuality, string> = {
  auto: 'Auto',
  high: 'High',
  medium: 'Medium',
  low: 'Low',
}
const OUTPUT_FORMATS: ImageOutputFormat[] = ['png', 'jpeg', 'webp']
const COUNTS = [1, 2, 3, 4]
const MAX_REFERENCE_IMAGES = 4
const IMAGE_ACCEPT = 'image/png,image/jpeg,image/webp'

function getImageSrc(image: GeneratedImage, outputFormat: ImageOutputFormat) {
  if (image.url) return image.url
  if (image.b64_json)
    return `data:image/${outputFormat};base64,${image.b64_json}`
  return ''
}

function getErrorMessage(error: unknown) {
  if (axios.isAxiosError(error)) {
    return (
      error.response?.data?.error?.message ||
      error.response?.data?.message ||
      error.message
    )
  }
  if (error instanceof Error) return error.message
  return 'Request failed'
}

function isTerminalTask(task: ImageGenerationTask | null) {
  return (
    task?.status === 'SUCCESS' ||
    task?.status === 'FAILURE' ||
    task?.status === 'CANCELLED'
  )
}

function taskImages(task: ImageGenerationTask | null) {
  return task?.response?.data ?? []
}

function taskMode(task: ImageGenerationTask): ImageMode {
  if (task.mode === 'edit' || task.action === 'edit') return 'edit'
  return 'generation'
}

function statusTone(status?: string) {
  if (status === 'SUCCESS') return 'text-emerald-600'
  if (status === 'FAILURE') return 'text-destructive'
  if (status === 'CANCELLED') return 'text-amber-600'
  return 'text-muted-foreground'
}

export function ImageGeneration() {
  const { t } = useTranslation()
  const [mode, setMode] = useState<ImageMode>('generation')
  const [prompt, setPrompt] = useState('')
  const [group, setGroup] = useState('')
  const [size, setSize] = useState<ImageSize>('1024x1024')
  const [quality, setQuality] = useState<ImageQuality>('auto')
  const [outputFormat, setOutputFormat] = useState<ImageOutputFormat>('png')
  const [count, setCount] = useState(1)
  const [referenceImages, setReferenceImages] = useState<File[]>([])
  const [mask, setMask] = useState<File | null>(null)
  const [referencePreviews, setReferencePreviews] = useState<
    { name: string; url: string }[]
  >([])
  const [maskPreview, setMaskPreview] = useState<{
    name: string
    url: string
  } | null>(null)
  const [images, setImages] = useState<GeneratedImage[]>([])
  const [activeTaskId, setActiveTaskId] = useState('')
  const [activeTask, setActiveTask] = useState<ImageGenerationTask | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [isCancelling, setIsCancelling] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const { data: groups = [] } = useQuery({
    queryKey: ['image-generation-groups'],
    queryFn: getUserGroups,
    staleTime: 5 * 60 * 1000,
  })

  const { data: recentTasks = [], refetch: refetchTasks } = useQuery({
    queryKey: ['image-generation-tasks'],
    queryFn: listImageTasks,
    staleTime: 20 * 1000,
  })

  useEffect(() => {
    if (group || groups.length === 0) return
    const preferred = groups.find((item) => item.value === PREFERRED_GROUP)
    setGroup(preferred?.value ?? groups[0]?.value ?? 'auto')
  }, [group, groups])

  useEffect(() => {
    const previews = referenceImages.map((file) => ({
      name: file.name,
      url: URL.createObjectURL(file),
    }))
    setReferencePreviews(previews)
    return () => {
      previews.forEach((item) => URL.revokeObjectURL(item.url))
    }
  }, [referenceImages])

  useEffect(() => {
    if (!mask) {
      setMaskPreview(null)
      return
    }
    const preview = {
      name: mask.name,
      url: URL.createObjectURL(mask),
    }
    setMaskPreview(preview)
    return () => URL.revokeObjectURL(preview.url)
  }, [mask])

  useEffect(() => {
    if (!activeTaskId) return

    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const poll = async () => {
      try {
        const task = await getImageTask(activeTaskId)
        if (cancelled) return

        setActiveTask(task)
        if (task.status === 'SUCCESS') {
          const nextImages = taskImages(task)
          setImages(nextImages)
          setError(nextImages.length === 0 ? t('No image data returned') : null)
          void refetchTasks()
          return
        }
        if (task.status === 'FAILURE') {
          setImages([])
          setError(task.fail_reason || t('Image generation failed'))
          void refetchTasks()
          return
        }
        if (task.status === 'CANCELLED') {
          setImages([])
          setError(null)
          void refetchTasks()
          return
        }

        timer = setTimeout(poll, 3000)
      } catch (err) {
        if (cancelled) return
        setError(getErrorMessage(err))
        timer = setTimeout(poll, 5000)
      }
    }

    void poll()

    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [activeTaskId, refetchTasks, t])

  const groupOptions = useMemo(() => {
    if (groups.length > 0) return groups
    return [{ label: 'auto', value: 'auto', ratio: 1 }]
  }, [groups])

  const taskRunning = Boolean(activeTask && !isTerminalTask(activeTask))
  const canCancelTask = taskRunning && !isCancelling
  const canGenerate =
    prompt.trim().length > 0 &&
    (mode === 'generation' || referenceImages.length > 0) &&
    !isSubmitting &&
    !taskRunning

  const handleReferenceImagesChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const files = Array.from(event.target.files ?? [])
      event.target.value = ''
      if (files.length === 0) return

      setReferenceImages((previous) => {
        const slots = MAX_REFERENCE_IMAGES - previous.length
        if (slots <= 0) {
          toast.error(t('Up to 4 reference images'))
          return previous
        }
        if (files.length > slots) {
          toast.error(t('Up to 4 reference images'))
        }
        return [...previous, ...files.slice(0, slots)]
      })
    },
    [t]
  )

  const handleRemoveReferenceImage = useCallback((index: number) => {
    setReferenceImages((previous) =>
      previous.filter((_, itemIndex) => itemIndex !== index)
    )
  }, [])

  const handleMaskChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0] ?? null
      event.target.value = ''
      if (!file) return
      setMask(file)
    },
    []
  )

  const handleGenerate = useCallback(async () => {
    const normalizedPrompt = prompt.trim()
    if (!normalizedPrompt) {
      toast.error(t('Prompt is required'))
      return
    }
    if (mode === 'edit' && referenceImages.length === 0) {
      toast.error(t('Reference image is required'))
      return
    }

    setIsSubmitting(true)
    setError(null)
    setImages([])

    try {
      const task =
        mode === 'edit'
          ? await editImage({
              model: MODEL,
              group,
              prompt: normalizedPrompt,
              n: count,
              size,
              quality,
              output_format: outputFormat,
              images: referenceImages,
              mask,
            })
          : await generateImage({
              model: MODEL,
              group,
              prompt: normalizedPrompt,
              n: count,
              size,
              quality,
              output_format: outputFormat,
            })

      setActiveTask(task)
      setActiveTaskId(task.task_id)
      toast.success(t('Image task submitted'))
      void refetchTasks()
    } catch (err) {
      setError(getErrorMessage(err))
    } finally {
      setIsSubmitting(false)
    }
  }, [
    count,
    group,
    mask,
    mode,
    outputFormat,
    prompt,
    quality,
    referenceImages,
    refetchTasks,
    size,
    t,
  ])

  const handleCancelTask = useCallback(async () => {
    if (!activeTask || isTerminalTask(activeTask)) return

    setIsCancelling(true)
    setError(null)

    try {
      const task = await cancelImageTask(activeTask.task_id)
      setActiveTask(task)
      setActiveTaskId(task.task_id)
      setImages(taskImages(task))
      if (task.status === 'CANCELLED') {
        toast.success(t('Image task cancelled'))
      }
      void refetchTasks()
    } catch (err) {
      setError(getErrorMessage(err))
    } finally {
      setIsCancelling(false)
    }
  }, [activeTask, refetchTasks, t])

  const handleSelectTask = useCallback((task: ImageGenerationTask) => {
    setActiveTask(task)
    setActiveTaskId(task.task_id)
    setMode(taskMode(task))
    setPrompt(task.prompt || '')
    if (task.size) setSize(task.size)
    if (task.output_format) setOutputFormat(task.output_format)
    setReferenceImages([])
    setMask(null)

    const nextImages = taskImages(task)
    setImages(nextImages)
    setError(task.status === 'FAILURE' ? task.fail_reason || null : null)
  }, [])

  const handleDownload = useCallback(
    (image: GeneratedImage, index: number) => {
      const src = getImageSrc(image, outputFormat)
      if (!src) return

      const link = document.createElement('a')
      link.href = src
      link.download = `${MODEL}-${index + 1}.${outputFormat}`
      link.rel = 'noopener'
      document.body.appendChild(link)
      link.click()
      link.remove()
    },
    [outputFormat]
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Image Generation')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Badge variant='secondary'>{MODEL}</Badge>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='grid min-h-0 gap-4 lg:grid-cols-[minmax(320px,420px)_1fr]'>
          <Card className='self-start rounded-lg' size='sm'>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'>
                <WandSparkles className='size-4' />
                {t('Generate Image')}
              </CardTitle>
            </CardHeader>
            <CardContent>
              <div className='space-y-4'>
                <Tabs
                  value={mode}
                  onValueChange={(value) => setMode(value as ImageMode)}
                >
                  <TabsList className='grid w-full grid-cols-2'>
                    <TabsTrigger value='generation' className='gap-1.5'>
                      <WandSparkles className='size-4' />
                      {t('Text to image')}
                    </TabsTrigger>
                    <TabsTrigger value='edit' className='gap-1.5'>
                      <Images className='size-4' />
                      {t('Image edit')}
                    </TabsTrigger>
                  </TabsList>
                  <TabsContent value='edit' className='mt-3 space-y-3'>
                    <div className='space-y-2'>
                      <div className='flex items-center justify-between gap-2'>
                        <Label htmlFor='image-reference-files'>
                          {t('Reference images')}
                        </Label>
                        <Badge variant='outline'>
                          {referenceImages.length} / {MAX_REFERENCE_IMAGES}
                        </Badge>
                      </div>
                      <Input
                        id='image-reference-files'
                        type='file'
                        accept={IMAGE_ACCEPT}
                        multiple
                        onChange={handleReferenceImagesChange}
                        disabled={
                          referenceImages.length >= MAX_REFERENCE_IMAGES ||
                          isSubmitting ||
                          taskRunning
                        }
                      />
                      {referencePreviews.length > 0 && (
                        <div className='grid grid-cols-2 gap-2'>
                          {referencePreviews.map((item, index) => (
                            <div
                              key={`${item.name}-${item.url}`}
                              className='bg-muted relative aspect-square overflow-hidden rounded-md border'
                            >
                              <img
                                src={item.url}
                                alt={item.name}
                                className='h-full w-full object-cover'
                              />
                              <Button
                                type='button'
                                variant='secondary'
                                size='icon-xs'
                                className='bg-background/90 absolute top-1 right-1'
                                onClick={() =>
                                  handleRemoveReferenceImage(index)
                                }
                                disabled={isSubmitting || taskRunning}
                              >
                                <Trash2 className='size-3' />
                              </Button>
                              <span className='bg-background/90 absolute inset-x-0 bottom-0 truncate px-1 py-0.5 text-xs'>
                                {item.name}
                              </span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>

                    <div className='space-y-2'>
                      <Label htmlFor='image-mask-file'>{t('Mask')}</Label>
                      <Input
                        id='image-mask-file'
                        type='file'
                        accept={IMAGE_ACCEPT}
                        onChange={handleMaskChange}
                        disabled={isSubmitting || taskRunning}
                      />
                      {maskPreview && (
                        <div className='flex items-center gap-2 rounded-md border p-2'>
                          <img
                            src={maskPreview.url}
                            alt={maskPreview.name}
                            className='bg-muted size-12 rounded object-cover'
                          />
                          <span className='min-w-0 flex-1 truncate text-sm'>
                            {maskPreview.name}
                          </span>
                          <Button
                            type='button'
                            variant='outline'
                            size='icon-sm'
                            onClick={() => setMask(null)}
                            disabled={isSubmitting || taskRunning}
                          >
                            <Trash2 className='size-4' />
                          </Button>
                        </div>
                      )}
                    </div>
                  </TabsContent>
                </Tabs>

                <div className='space-y-2'>
                  <Label htmlFor='image-prompt'>{t('Prompt')}</Label>
                  <Textarea
                    id='image-prompt'
                    value={prompt}
                    onChange={(event) => setPrompt(event.target.value)}
                    placeholder={t('Describe the image you want')}
                    className='min-h-32 resize-y'
                  />
                </div>

                <div className='grid gap-3 sm:grid-cols-2'>
                  <div className='space-y-2'>
                    <Label htmlFor='image-group'>{t('Group')}</Label>
                    <NativeSelect
                      id='image-group'
                      className='w-full'
                      value={group}
                      onChange={(event) => setGroup(event.target.value)}
                    >
                      {groupOptions.map((item) => (
                        <NativeSelectOption key={item.value} value={item.value}>
                          {item.label}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>

                  <div className='space-y-2'>
                    <Label htmlFor='image-size'>{t('Size')}</Label>
                    <NativeSelect
                      id='image-size'
                      className='w-full'
                      value={size}
                      onChange={(event) =>
                        setSize(event.target.value as ImageSize)
                      }
                    >
                      {SIZES.map((item) => (
                        <NativeSelectOption key={item} value={item}>
                          {item}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>

                  <div className='space-y-2'>
                    <Label htmlFor='image-quality'>{t('Quality')}</Label>
                    <NativeSelect
                      id='image-quality'
                      className='w-full'
                      value={quality}
                      onChange={(event) =>
                        setQuality(event.target.value as ImageQuality)
                      }
                    >
                      {QUALITIES.map((item) => (
                        <NativeSelectOption key={item} value={item}>
                          {t(QUALITY_LABELS[item])}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>

                  <div className='space-y-2'>
                    <Label htmlFor='image-count'>{t('Count')}</Label>
                    <NativeSelect
                      id='image-count'
                      className='w-full'
                      value={String(count)}
                      onChange={(event) => setCount(Number(event.target.value))}
                    >
                      {COUNTS.map((item) => (
                        <NativeSelectOption key={item} value={item}>
                          {item}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>

                  <div className='space-y-2 sm:col-span-2'>
                    <Label htmlFor='image-format'>{t('Output format')}</Label>
                    <NativeSelect
                      id='image-format'
                      className='w-full'
                      value={outputFormat}
                      onChange={(event) =>
                        setOutputFormat(event.target.value as ImageOutputFormat)
                      }
                    >
                      {OUTPUT_FORMATS.map((item) => (
                        <NativeSelectOption key={item} value={item}>
                          {item.toUpperCase()}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>
                </div>

                {activeTask && (
                  <Alert>
                    <AlertDescription>
                      <div className='flex items-center gap-2'>
                        {activeTask.status === 'SUCCESS' ? (
                          <CheckCircle2 className='size-4 text-emerald-600' />
                        ) : activeTask.status === 'FAILURE' ? (
                          <XCircle className='text-destructive size-4' />
                        ) : activeTask.status === 'CANCELLED' ? (
                          <XCircle className='size-4 text-amber-600' />
                        ) : (
                          <Clock3 className='text-muted-foreground size-4' />
                        )}
                        <span className={statusTone(activeTask.status)}>
                          {t(activeTask.status)}
                        </span>
                        <span className='text-muted-foreground'>
                          {activeTask.progress || '0%'}
                        </span>
                      </div>
                    </AlertDescription>
                  </Alert>
                )}

                {error && (
                  <Alert variant='destructive'>
                    <AlertDescription>{error}</AlertDescription>
                  </Alert>
                )}

                <div className='flex flex-wrap gap-2'>
                  <Button onClick={handleGenerate} disabled={!canGenerate}>
                    {isSubmitting || taskRunning ? (
                      <Loader2 className='size-4 animate-spin' />
                    ) : mode === 'edit' ? (
                      <Images className='size-4' />
                    ) : (
                      <ImageIcon className='size-4' />
                    )}
                    {t(
                      isSubmitting
                        ? 'Submitting...'
                        : taskRunning
                          ? 'Generating...'
                          : mode === 'edit'
                            ? 'Edit'
                            : 'Generate'
                    )}
                  </Button>
                  {activeTask && !isTerminalTask(activeTask) && (
                    <Button
                      variant='outline'
                      onClick={handleCancelTask}
                      disabled={!canCancelTask}
                    >
                      {isCancelling ? (
                        <Loader2 className='size-4 animate-spin' />
                      ) : (
                        <XCircle className='size-4' />
                      )}
                      {t(isCancelling ? 'Cancelling...' : 'Cancel')}
                    </Button>
                  )}
                  <Button
                    variant='outline'
                    onClick={() => {
                      setMode('generation')
                      setPrompt('')
                      setReferenceImages([])
                      setMask(null)
                      setImages([])
                      setActiveTask(null)
                      setActiveTaskId('')
                      setError(null)
                    }}
                    disabled={isSubmitting || isCancelling}
                  >
                    <RotateCcw className='size-4' />
                    {t('Reset')}
                  </Button>
                </div>

                {recentTasks.length > 0 && (
                  <div className='space-y-2'>
                    <Label>{t('Recent Tasks')}</Label>
                    <div className='max-h-48 space-y-1 overflow-y-auto'>
                      {recentTasks.map((task) => (
                        <button
                          key={task.task_id}
                          type='button'
                          onClick={() => handleSelectTask(task)}
                          className='hover:bg-muted flex w-full items-center justify-between gap-2 rounded-md border px-2 py-1.5 text-left text-sm'
                        >
                          <span className='flex min-w-0 items-center gap-2'>
                            <Badge variant='secondary'>
                              {taskMode(task) === 'edit'
                                ? t('Edit')
                                : t('Text')}
                            </Badge>
                            <span className='truncate'>{task.prompt}</span>
                          </span>
                          <Badge variant='outline'>{t(task.status)}</Badge>
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </CardContent>
          </Card>

          <div className='bg-muted/20 min-h-[420px] rounded-lg border p-3'>
            {images.length === 0 ? (
              <div className='flex h-full min-h-[380px] items-center justify-center rounded-md border border-dashed'>
                {taskRunning ? (
                  <div className='text-muted-foreground flex items-center gap-2 text-sm'>
                    <Loader2 className='size-5 animate-spin' />
                    {t('Waiting for image generation')}
                  </div>
                ) : (
                  <ImageIcon className='text-muted-foreground size-10' />
                )}
              </div>
            ) : (
              <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-3'>
                {images.map((image, index) => {
                  const src = getImageSrc(image, outputFormat)
                  return (
                    <Card
                      key={`${src}-${index}`}
                      className='rounded-lg'
                      size='sm'
                    >
                      <div className='bg-muted aspect-square overflow-hidden'>
                        {src ? (
                          <img
                            src={src}
                            alt={image.revised_prompt || prompt}
                            className='h-full w-full object-contain'
                          />
                        ) : (
                          <div className='flex h-full items-center justify-center'>
                            <ImageIcon className='text-muted-foreground size-8' />
                          </div>
                        )}
                      </div>
                      <CardContent>
                        <div className='flex items-center justify-between gap-2'>
                          <Badge variant='outline'>
                            {index + 1} / {images.length}
                          </Badge>
                          <Button
                            variant='outline'
                            size='sm'
                            onClick={() => handleDownload(image, index)}
                            disabled={!src}
                          >
                            <Download className='size-4' />
                            {t('Download')}
                          </Button>
                        </div>
                      </CardContent>
                    </Card>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
