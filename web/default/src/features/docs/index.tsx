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
import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import {
  BookOpen,
  CheckCircle2,
  ExternalLink,
  FileJson2,
  FolderOpen,
  KeyRound,
  Monitor,
  ShieldAlert,
  Terminal,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/copy-button'
import { PublicLayout } from '@/components/layout'
import { Footer } from '@/components/layout/components/footer'

const API_BASE_URL = 'https://api.cmsg666.xyz/v1'

const authJson = `{
  "OPENAI_API_KEY": "自己的秘钥"
}`

const simpleConfig = `model = "gpt-5.5"
model_reasoning_effort = "high"
disable_response_storage = true
model_provider = "cmsg"

[history]
persistence = "save-all"

[model_providers.cmsg]
name = "cmsg"
base_url = "${API_BASE_URL}"
wire_api = "responses"
requires_openai_auth = true`

const recommendedConfig = `model = "gpt-5.5"
model_reasoning_effort = "high"
disable_response_storage = true
sandbox_mode = "workspace-write"
approval_policy = "on-request"
windows_wsl_setup_acknowledged = true
file_opener = "vscode"
model_provider = "cmsg"
web_search = "cached"
suppress_unstable_features_warning = true

[history]
persistence = "save-all"

[tui]
notifications = true

[shell_environment_policy]
inherit = "all"
ignore_default_excludes = false

[sandbox_workspace_write]
network_access = true

[features]
plan_tool = true
apply_patch_freeform = true
view_image_tool = true
rmcp_client = true

[model_providers.cmsg]
name = "cmsg"
base_url = "${API_BASE_URL}"
wire_api = "responses"
requires_openai_auth = true`

const installCommands = `# Windows 桌面端
winget install Codex -s msstore

# Windows PowerShell 终端版
irm https://chatgpt.com/codex/install.ps1 | iex

# WSL / macOS / Linux 终端版
curl -fsSL https://chatgpt.com/codex/install.sh | sh

# 安装后进入项目目录运行
codex`

const smokeTestCommands = `# 1. 确认命令可用
codex --version

# 2. 进入你的项目目录
cd 你的项目目录

# 3. 启动 Codex
codex`

const codexPaths = [
  {
    system: 'Windows',
    path: String.raw`C:\Users\你的用户名\.codex`,
    tip: '用户名就是资源管理器地址栏里的那一段，不是电脑名。',
  },
  {
    system: 'macOS',
    path: '~/.codex',
    tip: 'Finder 可用“前往文件夹”输入这个路径。',
  },
  {
    system: 'Linux / WSL',
    path: '~/.codex',
    tip: '终端里可运行 mkdir -p ~/.codex 创建目录。',
  },
]

const quickSteps: Array<{
  title: string
  description: string
  icon: LucideIcon
}> = [
  {
    title: '注册或登录本站',
    description: '先注册账号并进入控制台，后续 API 密钥和配置都在这里完成。',
    icon: BookOpen,
  },
  {
    title: '创建 API 密钥',
    description: '在 API 秘钥页面创建一个密钥。密钥通常以 sk- 开头，只显示一次。',
    icon: KeyRound,
  },
  {
    title: '安装 Codex',
    description: '桌面端适合日常交互，终端版适合在项目目录里直接工作。',
    icon: Monitor,
  },
  {
    title: '找到 .codex 目录',
    description:
      '桌面端和终端版都会读取这个目录里的 config.toml，文件不存在就新建。',
    icon: FolderOpen,
  },
  {
    title: '写入配置文件',
    description: `把 API 密钥写进 auth.json，再把 ${API_BASE_URL} 写进 config.toml。`,
    icon: FileJson2,
  },
]

function Section(props: {
  id: string
  eyebrow?: string
  title: string
  description?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section
      id={props.id}
      className={cn('scroll-mt-24 py-10', props.className)}
    >
      {props.eyebrow && (
        <p className='text-muted-foreground mb-2 text-sm font-medium'>
          {props.eyebrow}
        </p>
      )}
      <div className='mb-6 max-w-3xl space-y-3'>
        <h2 className='text-2xl font-semibold md:text-3xl'>{props.title}</h2>
        {props.description && (
          <div className='text-muted-foreground leading-7'>
            {props.description}
          </div>
        )}
      </div>
      {props.children}
    </section>
  )
}

function CodeBlock(props: { title: string; code: string; language?: string }) {
  return (
    <div className='border-border bg-card overflow-hidden rounded-lg border'>
      <div className='border-border flex items-center justify-between gap-3 border-b px-4 py-2.5'>
        <div className='min-w-0'>
          <p className='truncate text-sm font-medium'>{props.title}</p>
          {props.language && (
            <p className='text-muted-foreground text-xs'>{props.language}</p>
          )}
        </div>
        <CopyButton
          value={props.code}
          variant='outline'
          size='icon'
          tooltip='复制配置'
          successTooltip='已复制'
          aria-label={`复制 ${props.title}`}
        />
      </div>
      <pre className='bg-muted/35 max-w-full overflow-x-auto p-4 text-sm leading-6'>
        <code>{props.code}</code>
      </pre>
    </div>
  )
}

function InfoBox(props: {
  tone?: 'default' | 'warning' | 'success'
  icon?: LucideIcon
  title: string
  children: ReactNode
}) {
  const Icon = props.icon ?? CheckCircle2
  const toneClass =
    props.tone === 'warning'
      ? 'border-amber-300/60 bg-amber-50 text-amber-950 dark:border-amber-500/35 dark:bg-amber-500/10 dark:text-amber-100'
      : props.tone === 'success'
        ? 'border-emerald-300/60 bg-emerald-50 text-emerald-950 dark:border-emerald-500/35 dark:bg-emerald-500/10 dark:text-emerald-100'
        : 'border-border bg-muted/35 text-foreground'

  return (
    <div className={cn('flex gap-3 rounded-lg border p-4', toneClass)}>
      <Icon className='mt-0.5 size-5 shrink-0' />
      <div className='min-w-0 space-y-1'>
        <p className='font-medium'>{props.title}</p>
        <div className='text-sm leading-6 opacity-85'>{props.children}</div>
      </div>
    </div>
  )
}

function StepCard(props: {
  index: number
  title: string
  description: string
  icon: LucideIcon
}) {
  const Icon = props.icon
  return (
    <div className='border-border bg-card rounded-lg border p-4'>
      <div className='mb-4 flex items-center gap-3'>
        <div className='bg-primary/10 text-primary flex size-9 items-center justify-center rounded-lg'>
          <Icon className='size-4' />
        </div>
        <span className='text-muted-foreground text-sm'>
          步骤 {props.index}
        </span>
      </div>
      <h3 className='mb-2 font-semibold'>{props.title}</h3>
      <p className='text-muted-foreground text-sm leading-6'>
        {props.description}
      </p>
    </div>
  )
}

export function Docs() {
  return (
    <PublicLayout showMainContainer={false}>
      <main className='min-h-screen'>
        <section className='border-border bg-background border-b px-4 pt-28 pb-12'>
          <div className='mx-auto grid max-w-6xl gap-8 lg:grid-cols-[minmax(0,1fr)_320px] lg:items-start'>
            <div className='min-w-0 space-y-6'>
              <div className='inline-flex items-center gap-2 rounded-lg border px-3 py-1 text-sm'>
                <BookOpen className='size-4' />
                CMSG API 新手文档
              </div>
              <div className='max-w-3xl space-y-4'>
                <h1 className='text-3xl leading-tight font-semibold md:text-5xl'>
                  从拿到 API 密钥到跑起 Codex
                </h1>
                <p className='text-muted-foreground text-base leading-7 md:text-lg'>
                  这页只讲最常用的本地 Codex 用法：桌面端、终端版、auth.json、
                  config.toml、模型名和 base_url。照着做完后，Codex 会通过本站
                  New API 网关请求模型。
                </p>
              </div>
              <div className='flex flex-wrap gap-3'>
                <Button render={<a href='/sign-up' />}>注册账号</Button>
                <Button variant='outline' render={<a href='/keys' />}>
                  创建 API 密钥
                </Button>
                <Button
                  variant='outline'
                  render={
                    <a
                      href='https://developers.openai.com/codex/'
                      target='_blank'
                      rel='noreferrer'
                    />
                  }
                >
                  官方 Codex 文档
                  <ExternalLink className='size-3.5' />
                </Button>
              </div>
            </div>
            <nav className='border-border bg-card rounded-lg border p-4 text-sm lg:sticky lg:top-20'>
              <p className='mb-3 font-medium'>快速跳转</p>
              <div className='grid gap-2'>
                <a className='hover:text-primary' href='#quickstart'>
                  1. 跑通顺序
                </a>
                <a className='hover:text-primary' href='#api-key'>
                  2. 创建 API 密钥
                </a>
                <a className='hover:text-primary' href='#install'>
                  3. 安装 Codex
                </a>
                <a className='hover:text-primary' href='#codex-home'>
                  4. 找到 .codex
                </a>
                <a className='hover:text-primary' href='#auth'>
                  5. auth.json
                </a>
                <a className='hover:text-primary' href='#config'>
                  6. config.toml
                </a>
                <a className='hover:text-primary' href='#check'>
                  7. 启动检查
                </a>
              </div>
            </nav>
          </div>
        </section>

        <div className='mx-auto max-w-6xl px-4'>
          <Section
            id='quickstart'
            eyebrow='Quick Start'
            title='最快的跑通路径'
          >
            <div className='grid gap-4 md:grid-cols-2 lg:grid-cols-5'>
              {quickSteps.map((step, index) => (
                <StepCard key={step.title} index={index + 1} {...step} />
              ))}
            </div>
          </Section>

          <Section
            id='api-key'
            eyebrow='API Key'
            title='创建 API 密钥'
            description='注册或登录本站后，先在 API 秘钥页面创建一个密钥，保存后再继续安装和本地配置。'
          >
            <figure className='border-border bg-card overflow-hidden rounded-lg border'>
              <img
                src='/docs/create-api-key.png'
                alt='创建 API 密钥界面中填写名称并选择分组的示意图'
                className='bg-background mx-auto max-h-[760px] w-auto max-w-full object-contain'
              />
              <figcaption className='text-muted-foreground border-border border-t px-4 py-3 text-sm'>
                创建 API 密钥时，名称可自定义，分组按实际用途选择；保存后复制密钥。
              </figcaption>
            </figure>
          </Section>

          <Section
            id='install'
            eyebrow='Install'
            title='桌面端和终端版怎么安装'
            description='桌面端适合日常交互和看历史线程；终端版适合在项目目录里直接工作。Windows 用户也可以在 WSL 里装终端版。'
          >
            <div className='grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]'>
              <CodeBlock
                title='安装命令'
                language='shell / powershell'
                code={installCommands}
              />
              <div className='grid gap-4'>
                <InfoBox icon={Monitor} title='桌面端'>
                  <a
                    href='https://openai.com/zh-Hans-CN/codex/'
                    target='_blank'
                    rel='noreferrer'
                    className='text-primary inline-flex items-center gap-1 font-medium underline-offset-4 hover:underline'
                  >
                    Codex 官方下载页面
                    <ExternalLink className='size-3.5' />
                  </a>
                </InfoBox>
                <InfoBox icon={Terminal} title='终端版'>
                  在项目目录运行 codex。Windows 上建议代码在 WSL 文件系统时，
                  就在 WSL 里运行 codex。
                </InfoBox>
              </div>
            </div>
          </Section>

          <Section
            id='codex-home'
            eyebrow='CODEX_HOME'
            title='找到自己的 .codex 目录'
            description='Windows、macOS、Linux 的原理一样：先找到当前系统用户，再进入这个用户下面的 .codex 文件夹。'
          >
            <div className='grid gap-6 lg:grid-cols-[minmax(0,1fr)_420px]'>
              <figure className='border-border bg-card overflow-hidden rounded-lg border'>
                <img
                  src='/docs/codex-config.png'
                  alt='Windows 资源管理器中 .codex 目录和用户名位置示意图'
                  className='w-full object-contain'
                />
                <figcaption className='text-muted-foreground border-border border-t px-4 py-3 text-sm'>
                  红框里的用户名要换成你自己的，auth.json 和 config.toml
                  都放在同一个 .codex 目录里。
                </figcaption>
              </figure>
              <div className='grid gap-3'>
                {codexPaths.map((item) => (
                  <div
                    key={item.system}
                    className='border-border bg-card rounded-lg border p-4'
                  >
                    <p className='mb-2 font-medium'>{item.system}</p>
                    <code className='bg-muted block overflow-x-auto rounded-md px-3 py-2 text-sm'>
                      {item.path}
                    </code>
                    <p className='text-muted-foreground mt-2 text-sm leading-6'>
                      {item.tip}
                    </p>
                  </div>
                ))}
              </div>
            </div>
          </Section>

          <Section
            id='auth'
            eyebrow='Authentication'
            title='auth.json 文件配置'
            description='把本站创建的 API 密钥写到 auth.json。'
          >
            <div className='grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]'>
              <CodeBlock title='auth.json' language='json' code={authJson} />
              <InfoBox tone='warning' icon={ShieldAlert} title='密钥怎么填'>
                把“自己的秘钥”替换成你在本站 API 秘钥页面复制的完整密钥。
                如果文件已经存在，先备份再改；如果不存在，就新建 auth.json。
              </InfoBox>
            </div>
          </Section>

          <Section
            id='config'
            eyebrow='Configuration'
            title='config.toml 文件配置'
            description='下面两份配置任选一份。'
          >
            <div className='grid gap-6'>
              <InfoBox tone='warning' icon={ShieldAlert} title='改配置前先备份'>
                修改 provider、模型或本地状态后，旧会话可能不在当前视图里显示。
                建议先复制一份 config.toml 作为备份，再覆盖配置。
              </InfoBox>
              <div className='grid gap-6 lg:grid-cols-2'>
                <CodeBlock
                  title='简化配置'
                  language='toml'
                  code={simpleConfig}
                />
                <CodeBlock
                  title='较完整的推荐配置'
                  language='toml'
                  code={recommendedConfig}
                />
              </div>
              <InfoBox
                tone='warning'
                icon={ShieldAlert}
                title='不建议新手默认全权限'
              >
                danger-full-access 加 approval_policy = "never" 会让 Codex
                在更大范围内直接读写和执行命令。除非你明确知道风险，否则优先使用
                workspace-write 和 on-request。
              </InfoBox>
            </div>
          </Section>

          <Section
            id='check'
            eyebrow='Check'
            title='启动前后怎么检查'
            description='配置完成后，先确认 codex 命令可用，再进入项目目录启动。若报 401，优先检查 auth.json 里的密钥；若报模型不可用，检查密钥所在分组是否支持该模型。'
          >
            <div className='grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]'>
              <CodeBlock
                title='启动检查'
                language='shell'
                code={smokeTestCommands}
              />
              <div className='grid gap-4'>
                <InfoBox tone='success' title='看到模型开始输出'>
                  说明密钥、base_url、模型名和分组基本都通了。
                </InfoBox>
                <InfoBox tone='warning' icon={ShieldAlert} title='常见错误'>
                  401 通常是密钥不对；404 或 model_not_found 通常是模型名、
                  分组或渠道没有配置；长时间无输出可能是上游正在排队或思考。
                </InfoBox>
              </div>
            </div>
          </Section>
        </div>
      </main>
      <Footer />
    </PublicLayout>
  )
}
