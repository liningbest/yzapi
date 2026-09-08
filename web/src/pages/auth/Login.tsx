import { startTransition, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, Dropdown, Form, Input, Typography } from 'antd';
import { ApiOutlined, BranchesOutlined, GlobalOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { authApi } from '@/api';
import type { NormalizedError } from '@/api';
import { useAuthStore } from '@/stores/auth';
import { LOCALES, useLocaleStore } from '@/stores/locale';
import { useThemeStore } from '@/stores/theme';
import { useSiteStore } from '@/stores/site';
import BrandMark from '@/components/BrandMark';
import { homeFor } from '@/layouts/guards';

export default function Login() {
  const { t } = useTranslation(['auth', 'common']);
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const setLocale = useLocaleStore((s) => s.setLocale);
  const locale = useLocaleStore((s) => s.locale);
  const dark = useThemeStore((s) => s.mode) === 'dark';
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const siteName = useSiteStore((s) => s.name);
  const baseUrl = useSiteStore((s) => s.baseUrl);
  useEffect(() => {
    void useSiteStore.getState().refresh();
  }, []);

  const onFinish = async (values: { username: string; password: string }) => {
    setLoading(true);
    setError(null);
    try {
      const res = await authApi.login(values.username.trim(), values.password);
      startTransition(() => {
        setAuth(res.token, res.user);
        if (res.user.must_change_password) navigate('/change-password', { replace: true });
        else navigate(homeFor(res.user.role), { replace: true });
      });
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
            <BrandMark size={24} color="#fafafa" stroke="#18181b" />
            <span className="yz-login-logo-name">{siteName}</span>
          </div>

          <div className="yz-login-hero-body">
            <h1 className="yz-login-headline">{t('auth:login.headline')}</h1>
            <p className="yz-login-desc">{t('auth:login.description')}</p>

            <div className="yz-login-code" aria-hidden>
              <div className="yz-login-code-bar">
                <span>curl</span>
                <span className="yz-login-code-path">POST /v1/chat/completions</span>
              </div>
              <pre>
                <span className="c-cmd">curl</span> {baseUrl}/chat/completions \{'\n'}
                {'  '}<span className="c-flag">-H</span> <span className="c-str">"Authorization: Bearer sk-your-key"</span> \{'\n'}
                {'  '}<span className="c-flag">-d</span> <span className="c-str">{`'{"model": "yz-auto",`}</span>{'\n'}
                {'       '}<span className="c-str">{`"messages": [{"role": "user", "content": "你好"}]}'`}</span>
              </pre>
            </div>

            <ul className="yz-login-points">
              <li>
                <ApiOutlined />
                <div>
                  <b>{t('auth:login.point1Title')}</b>
                  <span>{t('auth:login.point1Desc')}</span>
                </div>
              </li>
              <li>
                <BranchesOutlined />
                <div>
                  <b>{t('auth:login.point2Title')}</b>
                  <span>{t('auth:login.point2Desc')}</span>
                </div>
              </li>
              <li>
                <SafetyCertificateOutlined />
                <div>
                  <b>{t('auth:login.point3Title')}</b>
                  <span>{t('auth:login.point3Desc')}</span>
                </div>
              </li>
            </ul>

            <div className="yz-login-facts">
              <div>
                <b>18</b>
                <span>{t('auth:login.factProviders')}</span>
              </div>
              <div>
                <b>6</b>
                <span>{t('auth:login.factEndpoints')}</span>
              </div>
              <div>
                <b>3</b>
                <span>{t('auth:login.factProtocols')}</span>
              </div>
              <div>
                <b>&lt; 3 ms</b>
                <span>{t('auth:login.factLatency')}</span>
              </div>
            </div>
          </div>

          <div className="yz-login-hero-foot">{t('auth:login.footer')}</div>
        </div>
      </aside>

      {/* ---------- right: form panel ---------- */}
      <main className="yz-login-panel">
        <div className="yz-login-panel-top">
          <Dropdown
            menu={{ items: LOCALES.map((l) => ({ key: l.key, label: l.label, onClick: () => setLocale(l.key) })) }}
            placement="bottomRight"
          >
            <Button type="text" size="small" icon={<GlobalOutlined />} style={{ color: 'var(--yz-text-secondary)' }}>
              {LOCALES.find((l) => l.key === locale)?.label}
            </Button>
          </Dropdown>
        </div>

        <div className="yz-login-form">
          <div className="yz-login-form-mobile-brand">
            <BrandMark size={22} color={dark ? '#fafafa' : '#18181b'} stroke={dark ? '#18181b' : '#ffffff'} />
            <span>{siteName}</span>
          </div>
          <h2 className="yz-login-title">{t('auth:login.title')}</h2>
          <p className="yz-login-subtitle">{t('auth:login.subtitle')}</p>

          {error ? <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} /> : null}

          <Form layout="vertical" onFinish={onFinish} requiredMark={false} autoComplete="off">
            <Form.Item
              name="username"
              label={t('auth:login.username')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <Input placeholder={t('auth:login.usernamePlaceholder')} autoFocus />
            </Form.Item>
            <Form.Item
              name="password"
              label={t('auth:login.password')}
              rules={[{ required: true, message: t('common:common.required') }]}
            >
              <Input.Password placeholder={t('auth:login.passwordPlaceholder')} />
            </Form.Item>
            <Button type="primary" htmlType="submit" block loading={loading} style={{ marginTop: 4, height: 36 }}>
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
