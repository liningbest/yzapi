import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, Dropdown, Form, Input, Typography } from 'antd';
import {
  ApiOutlined,
  BranchesOutlined,
  GlobalOutlined,
  LockOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { authApi } from '@/api';
import type { NormalizedError } from '@/api';
import { useAuthStore } from '@/stores/auth';
import { LOCALES, useLocaleStore } from '@/stores/locale';
import BrandMark from '@/components/BrandMark';
import { homeFor } from '@/layouts/guards';

const FEATURES = [
  { key: 'feature1', icon: <ApiOutlined /> },
  { key: 'feature2', icon: <BranchesOutlined /> },
  { key: 'feature3', icon: <SafetyCertificateOutlined /> },
  { key: 'feature4', icon: <ThunderboltOutlined /> },
] as const;

export default function Login() {
  const { t } = useTranslation(['auth', 'common']);
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const setLocale = useLocaleStore((s) => s.setLocale);
  const locale = useLocaleStore((s) => s.locale);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true);
    setError(null);
    try {
      const res = await authApi.login(values.username.trim(), values.password);
      setAuth(res.token, res.user);
      if (res.user.must_change_password) navigate('/change-password', { replace: true });
      else navigate(homeFor(res.user.role), { replace: true });
    } catch (e) {
      const err = e as NormalizedError;
      if (err.status === 423) setError(t('auth:login.locked'));
      else if (err.status === 401) setError(t('auth:login.failed'));
      else if (err.status === 403) setError(t('auth:login.disabled'));
      else setError(err.message || t('common:error.network'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="yz-login">
      {/* ---------- left: brand panel ---------- */}
      <aside className="yz-login-hero">
        <div className="yz-login-hero-inner">
          <div className="yz-login-logo">
            <BrandMark size={44} />
            <div>
              <div className="yz-login-logo-name">{t('common:app.name')}</div>
              <div className="yz-login-logo-sub">{t('auth:login.tagline')}</div>
            </div>
          </div>

          <div className="yz-login-hero-body">
            <h1 className="yz-login-headline">{t('auth:login.headline')}</h1>
            <p className="yz-login-desc">{t('auth:login.description')}</p>
            <ul className="yz-login-features">
              {FEATURES.map((f) => (
                <li key={f.key}>
                  <span className="yz-login-feature-icon">{f.icon}</span>
                  <span>{t(`auth:login.${f.key}`)}</span>
                </li>
              ))}
            </ul>
          </div>

          <div className="yz-login-hero-foot">
            <span className="yz-login-chip">OpenAI</span>
            <span className="yz-login-chip">Anthropic</span>
            <span className="yz-login-chip">DeepSeek</span>
            <span className="yz-login-chip">Qwen</span>
            <span className="yz-login-chip">vLLM</span>
            <span className="yz-login-chip">+ 13</span>
          </div>
        </div>
      </aside>

      {/* ---------- right: form panel ---------- */}
      <main className="yz-login-panel">
        <div className="yz-login-panel-top">
          <Dropdown
            menu={{ items: LOCALES.map((l) => ({ key: l.key, label: l.label, onClick: () => setLocale(l.key) })) }}
            placement="bottomRight"
          >
            <Button type="text" icon={<GlobalOutlined />}>
              {LOCALES.find((l) => l.key === locale)?.label}
            </Button>
          </Dropdown>
        </div>

        <div className="yz-login-form">
          <div className="yz-login-form-mobile-brand">
            <BrandMark size={36} />
            <span>{t('common:app.name')}</span>
          </div>
          <Typography.Title level={2} style={{ margin: 0 }}>
            {t('auth:login.title')}
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 28 }}>
            {t('auth:login.subtitle')}
          </Typography.Paragraph>

          {error ? <Alert type="error" showIcon message={error} style={{ marginBottom: 20 }} /> : null}

          <Form layout="vertical" onFinish={onFinish} size="large" requiredMark={false} autoComplete="off">
            <Form.Item
              name="username"
              label={t('auth:login.username')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <Input prefix={<UserOutlined />} placeholder={t('auth:login.usernamePlaceholder')} autoFocus />
            </Form.Item>
            <Form.Item
              name="password"
              label={t('auth:login.password')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <Input.Password prefix={<LockOutlined />} placeholder={t('auth:login.passwordPlaceholder')} />
            </Form.Item>
            <Button type="primary" htmlType="submit" block loading={loading} style={{ marginTop: 8, height: 44 }}>
              {t('auth:login.submit')}
            </Button>
          </Form>

          <Typography.Text type="secondary" className="yz-login-hint">
            {t('auth:login.hint')}
          </Typography.Text>
        </div>

        <div className="yz-login-panel-foot">
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('auth:login.footer')}
          </Typography.Text>
        </div>
      </main>
    </div>
  );
}
