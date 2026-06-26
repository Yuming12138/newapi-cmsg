/*
Copyright (C) 2025 QuantumNous

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

import React, { useEffect, useMemo, useState } from 'react';
import { Button, Modal, Space, Spin, Tabs, Tag, Typography } from '@douyinfe/semi-ui';
import {
  buildClaudeCodeSettingsJson,
  buildCodexConfigToml,
  getClaudeBaseUrl,
  getOpenAIBaseUrl,
  getServerAddress,
} from '../../../../helpers/token';

const { Text } = Typography;

const codeBlockStyle = {
  margin: 0,
  padding: 12,
  maxHeight: 320,
  overflow: 'auto',
  borderRadius: 6,
  background: 'var(--semi-color-fill-0)',
  border: '1px solid var(--semi-color-border)',
  fontFamily: 'Consolas, Monaco, "Courier New", monospace',
  fontSize: 12,
  lineHeight: 1.6,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
};

const fieldStyle = {
  minWidth: 0,
  padding: '8px 10px',
  borderRadius: 6,
  background: 'var(--semi-color-fill-0)',
};

function ConfigBlock({ title, content, copyText, t }) {
  return (
    <div>
      <div className='flex justify-between items-center mb-2 gap-2'>
        <Text strong>{title}</Text>
        <Button size='small' type='primary' onClick={() => copyText(content)}>
          {t('复制')}
        </Button>
      </div>
      <pre style={codeBlockStyle}>{content}</pre>
    </div>
  );
}

export default function TokenConfigModal({
  visible,
  onClose,
  token,
  fetchTokenKey,
  copyText,
  t,
}) {
  const [tokenKey, setTokenKey] = useState('');
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState('');

  useEffect(() => {
    let cancelled = false;
    if (!visible || !token?.id) {
      setTokenKey('');
      setLoadError('');
      return () => {};
    }

    setLoading(true);
    setLoadError('');
    fetchTokenKey(token, { suppressError: true })
      .then((key) => {
        if (!cancelled) setTokenKey(`sk-${key}`);
      })
      .catch((error) => {
        if (!cancelled) {
          setTokenKey('');
          setLoadError(error?.message || t('获取令牌密钥失败'));
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [visible, token, fetchTokenKey, t]);

  const serverAddress = getServerAddress();
  const openAIBaseUrl = getOpenAIBaseUrl(serverAddress);
  const claudeBaseUrl = getClaudeBaseUrl(serverAddress);

  const codexConfig = useMemo(
    () => buildCodexConfigToml(serverAddress, token?.group),
    [serverAddress, token?.group],
  );
  const codexApiKeyEnv = tokenKey ? `OPENAI_API_KEY="${tokenKey}"` : '';
  const claudeSettings = useMemo(
    () => buildClaudeCodeSettingsJson(tokenKey, serverAddress, token?.group),
    [serverAddress, token?.group, tokenKey],
  );

  return (
    <Modal
      title={t('复制配置文件')}
      icon={null}
      visible={visible}
      onCancel={onClose}
      footer={<Button onClick={onClose}>{t('关闭')}</Button>}
      style={{ maxWidth: '92vw' }}
      width={760}
    >
      <Spin spinning={loading}>
        <div className='space-y-4'>
          <div className='grid grid-cols-1 md:grid-cols-2 gap-2'>
            <div style={fieldStyle}>
              <Text type='tertiary' size='small'>
                {t('OpenAI / Codex Base URL')}
              </Text>
              <div className='mt-1'>
                <Text code copyable={{ content: openAIBaseUrl }}>
                  {openAIBaseUrl}
                </Text>
              </div>
            </div>
            <div style={fieldStyle}>
              <Text type='tertiary' size='small'>
                {t('Claude Code Base URL')}
              </Text>
              <div className='mt-1'>
                <Text code copyable={{ content: claudeBaseUrl }}>
                  {claudeBaseUrl}
                </Text>
              </div>
            </div>
            <div style={fieldStyle} className='md:col-span-2'>
              <Text type='tertiary' size='small'>
                {t('API Key')}
              </Text>
              <div className='mt-1 break-all'>
                {loadError ? (
                  <Text type='danger'>{loadError}</Text>
                ) : tokenKey ? (
                  <Text code copyable={{ content: tokenKey }}>
                    {tokenKey}
                  </Text>
                ) : (
                  <Text type='tertiary'>{t('加载中...')}</Text>
                )}
              </div>
            </div>
          </div>

          <Tabs type='card' defaultActiveKey='codex'>
            <Tabs.TabPane tab='Codex config.toml' itemKey='codex'>
              <Space vertical align='start' className='w-full'>
                <Tag color='blue' shape='circle'>
                  config.toml
                </Tag>
                <ConfigBlock
                  title='config.toml'
                  content={codexConfig}
                  copyText={copyText}
                  t={t}
                />
                {codexApiKeyEnv && (
                  <ConfigBlock
                    title='OPENAI_API_KEY'
                    content={codexApiKeyEnv}
                    copyText={copyText}
                    t={t}
                  />
                )}
              </Space>
            </Tabs.TabPane>
            <Tabs.TabPane tab='Claude Code settings.json' itemKey='claude'>
              <Space vertical align='start' className='w-full'>
                <Tag color='violet' shape='circle'>
                  settings.json
                </Tag>
                <ConfigBlock
                  title='settings.json'
                  content={claudeSettings}
                  copyText={copyText}
                  t={t}
                />
              </Space>
            </Tabs.TabPane>
          </Tabs>
        </div>
      </Spin>
    </Modal>
  );
}
