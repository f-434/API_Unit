import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Form, Input, Tag } from 'antd'
import { LockOutlined, ApiOutlined } from '@ant-design/icons'
import { api } from '../api'

export default function Login() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data } = useQuery({ queryKey: ['authStatus'], queryFn: api.authStatus })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (data && !data.authEnabled) {
      navigate('/', { replace: true })
    }
  }, [data, navigate])

  const submit = async (values: { password: string }) => {
    setLoading(true)
    setError('')
    try {
      await api.login(values.password)
      await qc.invalidateQueries({ queryKey: ['authStatus'] })
      navigate('/', { replace: true })
    } catch (e: any) {
      setError(e?.message || '登录失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-wrap">
      <Card className="login-card" bordered={false} bodyStyle={{ padding: 30 }}>
        <div className="login-brand">
          <div className="logo"><ApiOutlined /></div>
          <div>
            <div style={{ fontSize: 18, fontWeight: 700 }}>API Unit</div>
            <div className="muted">LLM 接口测试台 · 本地独立运行</div>
          </div>
        </div>
        <Alert
          type="info"
          showIcon
          message="访问受密码保护"
          description="请输入服务端 AUTH_PASSWORD 配置的管理密码。"
          style={{ marginBottom: 18 }}
        />
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 14 }} />}
        <Form layout="vertical" onFinish={submit}>
          <Form.Item name="password" label="管理密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password size="large" prefix={<LockOutlined />} autoFocus placeholder="AUTH_PASSWORD" />
          </Form.Item>
          <Button type="primary" size="large" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
        <div style={{ marginTop: 16, textAlign: 'center' }}>
          <Tag>数据仅保存在本机 SQLite</Tag>
        </div>
      </Card>
    </div>
  )
}
