import React from 'react'
import ReactDOM from 'react-dom/client'
import { App as AntApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import dayjs from 'dayjs'
import 'dayjs/locale/zh-cn'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import './styles.css'

dayjs.locale('zh-cn')

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
      staleTime: 5000,
    },
  },
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#165dff',
          colorInfo: '#165dff',
          colorLink: '#165dff',
          colorText: '#1d2129',
          colorTextSecondary: '#4e5969',
          colorBorder: '#e5e6eb',
          colorBorderSecondary: '#f0f1f3',
          colorBgLayout: '#f5f7fa',
          borderRadius: 8,
          fontSize: 13,
        },
        components: {
          Layout: {
            siderBg: '#ffffff',
            headerBg: '#ffffff',
            bodyBg: '#f5f7fa',
            headerHeight: 56,
            headerPadding: '0 24px',
          },
          Menu: {
            itemBg: 'transparent',
            itemColor: '#4e5969',
            itemHoverBg: '#f2f3f5',
            itemHoverColor: '#1d2129',
            itemSelectedBg: '#e8f3ff',
            itemSelectedColor: '#165dff',
            itemBorderRadius: 8,
            itemHeight: 40,
            itemMarginBlock: 4,
            itemMarginInline: 0,
          },
          Table: {
            headerBg: '#fafbfc',
            headerColor: '#4e5969',
            rowHoverBg: '#f7f8fa',
            borderColor: '#f0f1f3',
            cellPaddingBlockSM: 7,
            cellPaddingInlineSM: 10,
          },
          Button: { primaryShadow: '0 4px 10px rgba(22, 93, 255, 0.18)' },
        },
      }}
    >
      <AntApp>
        <QueryClientProvider client={queryClient}>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  </React.StrictMode>,
)
