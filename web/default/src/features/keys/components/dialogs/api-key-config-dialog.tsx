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

import { Copy } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { useStatus } from '@/hooks/use-status'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  buildClaudeCodeSettingsJson,
  buildCodexConfigToml,
  getClaudeBaseUrl,
  getOpenAIBaseUrl,
  getServerAddress,
} from '../../lib/client-config'
import { type ApiKey } from '../../types'

function ConfigBlock({
  title,
  content,
}: {
  title: string
  content: string
}) {
  const { t } = useTranslation()

  const handleCopy = async () => {
    const ok = await copyToClipboard(content)
    if (ok) toast.success(t('Copied'))
  }

  return (
    <div className='space-y-2'>
      <div className='flex items-center justify-between gap-2'>
        <Label>{title}</Label>
        <Button variant='outline' size='sm' onClick={handleCopy}>
          <Copy className='mr-1.5 size-3.5' />
          {t('Copy')}
        </Button>
      </div>
      <pre className='bg-muted/50 max-h-80 overflow-auto rounded-md border p-3 font-mono text-xs leading-6 whitespace-pre-wrap break-words'>
        {content}
      </pre>
    </div>
  )
}

function CopyableField({ label, value }: { label: string; value: string }) {
  const { t } = useTranslation()

  const handleCopy = async () => {
    const ok = await copyToClipboard(value)
    if (ok) toast.success(t('Copied'))
  }

  return (
    <div className='bg-muted/40 min-w-0 rounded-md border p-3'>
      <div className='text-muted-foreground mb-1 text-xs'>{label}</div>
      <button
        type='button'
        onClick={handleCopy}
        className='w-full text-left font-mono text-xs break-all'
      >
        {value}
      </button>
    </div>
  )
}

type ApiKeyConfigDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  apiKey: ApiKey | null
  tokenKey: string
}

export function ApiKeyConfigDialog({
  open,
  onOpenChange,
  apiKey,
  tokenKey,
}: ApiKeyConfigDialogProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const serverAddress = getServerAddress(status)
  const openAIBaseUrl = getOpenAIBaseUrl(serverAddress)
  const claudeBaseUrl = getClaudeBaseUrl(serverAddress)
  const codexConfig = useMemo(
    () => buildCodexConfigToml(serverAddress, apiKey?.group),
    [apiKey?.group, serverAddress]
  )
  const codexApiKeyEnv = tokenKey ? `OPENAI_API_KEY="${tokenKey}"` : ''
  const claudeSettings = useMemo(
    () => buildClaudeCodeSettingsJson(tokenKey, serverAddress, apiKey?.group),
    [apiKey?.group, serverAddress, tokenKey]
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Copy client config')}</DialogTitle>
        </DialogHeader>

        <div className='space-y-4'>
          <div className='grid gap-2 md:grid-cols-2'>
            <CopyableField label='OpenAI / Codex Base URL' value={openAIBaseUrl} />
            <CopyableField label='Claude Code Base URL' value={claudeBaseUrl} />
            <div className='md:col-span-2'>
              <CopyableField label='API Key' value={tokenKey} />
            </div>
          </div>

          <Tabs defaultValue='codex'>
            <TabsList className='grid w-full grid-cols-2'>
              <TabsTrigger value='codex'>Codex config.toml</TabsTrigger>
              <TabsTrigger value='claude'>Claude Code settings.json</TabsTrigger>
            </TabsList>
            <TabsContent value='codex' className='space-y-3'>
              <Badge variant='outline'>config.toml</Badge>
              <ConfigBlock title='config.toml' content={codexConfig} />
              {codexApiKeyEnv && (
                <ConfigBlock title='OPENAI_API_KEY' content={codexApiKeyEnv} />
              )}
            </TabsContent>
            <TabsContent value='claude' className='space-y-3'>
              <Badge variant='outline'>settings.json</Badge>
              <ConfigBlock title='settings.json' content={claudeSettings} />
            </TabsContent>
          </Tabs>
        </div>

        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            {t('Close')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
