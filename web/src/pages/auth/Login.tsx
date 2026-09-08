import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, Card, Dropdown, Form, Input, Typography } from 'antd';
import { CheckCircleOutlined, GlobalOutlined, LockOutlined, UserOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { authApi } from '@/api';
import type { NormalizedError } from '@/api';
import { useAuthStore } from '@/stores/auth';
import { LOCALES, useLocaleStore } from '@/stores/locale';
import BrandMark from '@/components/BrandMark';
import { homeFor } from '@/layouts/guards';

export default function Login() {
  const { t } = useTranslation(['auth', 'common']);
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const setLocale = useLocaleStore((s) => s.setLocale);
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
    <div className="yz-auth-bg">
      <div className="yz-auth-features">
        {(['feature1', 'feature2', 'feature3'] as const).map((k) => (
          <div key={k}>
            <CheckCircleOutlined style={{ color: '#06b6d4', marginRight: 8 }} />
            {t(`auth:login.${k}`)}
          </div>
        ))}
      </div>
      <div style={{ position: 'absolute', top: 16, right: 16, zIndex: 2 }}>
        <Dropdown
          menu={{ items: LOCALES.map((l) => ({ key: l.key, label: l.label, onClick: () => setLocale(l.key) })) }}
          placement="bottomRight"
        >
          <Button type="text" icon={<GlobalOutlined />} style={{ color: 'rgba(255,255,255,0.8)' }} />
        </Dropdown>
      </div>
      <Card className="yz-auth-card" styles={{ body: { padding: 32 } }}>
        <div className="yz-auth-brand">
          <BrandMark size={40} />
          <div>
            <Typography.Title level={4} style={{ margin: 0 }}>
              {t('auth:login.title')}
            </Typography.Title>
            <Typography.Text type="secondary">{t('auth:login.subtitle')}</Typography.Text>
          </div>
        </div>
        {error ? <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} /> : null}
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
          <Button type="primary" htmlType="submit" block loading={loading} style={{ marginTop: 8 }}>
            {t('auth:login.submit')}
          </Button>
        </Form>
      </Card>
    </div>
  );
}
