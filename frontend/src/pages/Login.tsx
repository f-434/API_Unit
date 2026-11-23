import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Form, Input } from 'antd'
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
          <div style={{ fontSize: 18, fontWeight: 700 }}>API Unit</div>
        </div>
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 14 }} />}
        <Form layout="vertical" onFinish={submit}>
          <Form.Item name="password" label="管理密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password size="large" prefix={<LockOutlined />} autoFocus placeholder="请输入管理密码" />
          </Form.Item>
          <Button type="primary" size="large" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  )
}
