import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Alert, Button, Card, Form, Input, Progress, Space, Typography, message } from 'antd';
import { ArrowLeftOutlined, LockOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { authApi } from '@/api';
import type { NormalizedError } from '@/api';
import { useAuthStore } from '@/stores/auth';
import BrandMark from '@/components/BrandMark';
import { homeFor } from '@/layouts/guards';

function strength(pwd: string): { score: number; level: 'weak' | 'medium' | 'strong' } {
  if (!pwd) return { score: 0, level: 'weak' };
  let s = 0;
  if (pwd.length >= 12) s += 1;
  if (pwd.length >= 16) s += 1;
  if (/[a-z]/.test(pwd) && /[A-Z]/.test(pwd)) s += 1;
  if (/\d/.test(pwd)) s += 1;
  if (/[^A-Za-z0-9]/.test(pwd)) s += 1;
  const level = s <= 2 ? 'weak' : s <= 3 ? 'medium' : 'strong';
  return { score: Math.min(100, (s / 5) * 100), level };
}

export default function ChangePassword() {
  const { t } = useTranslation(['auth', 'common']);
  const navigate = useNavigate();
  const user = useAuthStore((s) => s.user);
  const clearAuth = useAuthStore((s) => s.clear);
  const forced = Boolean(user?.must_change_password);
  const [form] = Form.useForm();
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const newPwd = Form.useWatch('new_password', form) as string | undefined;
  const st = strength(newPwd ?? '');
  const strokeColor = st.level === 'strong' ? '#10b981' : st.level === 'medium' ? '#f59e0b' : '#ef4444';

  const onFinish = async (v: { old_password: string; new_password: string }) => {
    setLoading(true);
    setError(null);
    try {
      await authApi.changePassword(v.old_password, v.new_password);
      message.success(t('auth:changePassword.success'));
      clearAuth();
      navigate('/login', { replace: true });
    } catch (e) {
      setError((e as NormalizedError).message);
    } finally {
      setLoading(false);
    }
  };

  const onLogout = async () => {
    try {
      await authApi.logout();
    } catch {
      /* ignore */
    }
    clearAuth();
    navigate('/login', { replace: true });
  };

  return (
    <div className="yz-auth-bg">
      <Card className="yz-auth-card" styles={{ body: { padding: 32 } }}>
        <div className="yz-auth-brand">
          <BrandMark size={40} />
          <div>
            <Typography.Title level={4} style={{ margin: 0 }}>
              {t('auth:changePassword.title')}
            </Typography.Title>
            <Typography.Text type="secondary">{user?.username}</Typography.Text>
          </div>
        </div>
        {forced ? <Alert type="warning" showIcon message={t('auth:changePassword.forcedHint')} style={{ marginBottom: 16 }} /> : null}
        {error ? <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} /> : null}
        <Form form={form} layout="vertical" onFinish={onFinish} size="large" requiredMark={false} autoComplete="off">
          <Form.Item
            name="old_password"
            label={t('auth:changePassword.old')}
            rules={[{ required: true, message: t('common:common.required') }]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('auth:changePassword.oldPlaceholder')} autoFocus />
          </Form.Item>
          <Form.Item
            name="new_password"
            label={t('auth:changePassword.new')}
            extra={
              newPwd ? (
                <Space direction="vertical" size={2} style={{ width: '100%', marginTop: 6 }}>
                  <Progress percent={st.score} showInfo={false} size="small" strokeColor={strokeColor} />
                  <span style={{ fontSize: 12 }}>
                    {t('auth:changePassword.strength')}: <span style={{ color: strokeColor }}>{t(`auth:changePassword.${st.level}`)}</span>
                    {' · '}
                    {t('auth:changePassword.strengthHint')}
                  </span>
                </Space>
              ) : (
                t('auth:changePassword.strengthHint')
              )
            }
            rules={[
              { required: true, message: t('common:common.required') },
              { min: 12, max: 128, message: t('auth:changePassword.length') },
              ({ getFieldValue }) => ({
                validator: (_, v: string) =>
                  v && v === getFieldValue('old_password')
                    ? Promise.reject(new Error(t('auth:changePassword.sameAsOld')))
                    : Promise.resolve(),
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('auth:changePassword.newPlaceholder')} />
          </Form.Item>
          <Form.Item
            name="confirm"
            label={t('auth:changePassword.confirm')}
            dependencies={['new_password']}
            rules={[
              { required: true, message: t('common:common.required') },
              ({ getFieldValue }) => ({
                validator: (_, v: string) =>
                  !v || v === getFieldValue('new_password')
                    ? Promise.resolve()
                    : Promise.reject(new Error(t('auth:changePassword.mismatch'))),
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('auth:changePassword.confirmPlaceholder')} />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading} style={{ marginTop: 8 }}>
            {t('auth:changePassword.submit')}
          </Button>
          <div style={{ marginTop: 12, textAlign: 'center' }}>
            {forced ? (
              <Button type="link" onClick={onLogout}>
                {t('auth:changePassword.logoutInstead')}
              </Button>
            ) : (
              <Button type="link" icon={<ArrowLeftOutlined />} onClick={() => navigate(homeFor(user?.role))}>
                {t('common:action.back')}
              </Button>
            )}
          </div>
        </Form>
      </Card>
    </div>
  );
}
