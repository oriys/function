var _a;
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import compression from 'vite-plugin-compression';
import { visualizer } from 'rollup-plugin-visualizer';
var apiTarget = (_a = process.env.FUNCTION_API_URL) !== null && _a !== void 0 ? _a : 'http://localhost:8080';
export default defineConfig({
    plugins: [
        react(),
        compression({
            algorithm: 'gzip',
            ext: '.gz',
        }),
        compression({
            algorithm: 'brotliCompress',
            ext: '.br',
        }),
        visualizer({
            open: false,
            filename: 'stats.html',
            gzipSize: true,
            brotliSize: true,
        }),
    ],
    resolve: {
        alias: {
            '@': path.resolve(__dirname, './src'),
        },
    },
    server: {
        port: 3000,
        proxy: {
            '/api': {
                target: apiTarget,
                changeOrigin: true,
                ws: true,
                configure: function (proxy, _options) {
                    proxy.on('error', function (err, _req, _res) {
                        console.log('proxy error', err);
                    });
                    proxy.on('proxyReqWs', function (proxyReq, req, socket) {
                        console.log('WebSocket proxy:', req.url);
                    });
                },
            },
        },
    },
    build: {
        outDir: 'dist',
        sourcemap: false,
        chunkSizeWarningLimit: 600, // 降低警告阈值以监控体积
        cssCodeSplit: true,
        rollupOptions: {
            output: {
                // 采用动态分包策略
                manualChunks: function (id) {
                    if (id.includes('node_modules')) {
                        // 将 echarts 及其相关库单独提取，因为它们很大
                        if (id.includes('echarts') || id.includes('zrender')) {
                            return 'vendor-charts';
                        }
                        // 将 react 核心库提取
                        if (id.includes('react') || id.includes('scheduler')) {
                            return 'vendor-core';
                        }
                        // 将 reactflow 提取
                        if (id.includes('reactflow') || id.includes('@reactflow')) {
                            return 'vendor-flow';
                        }
                        // 其他第三方库
                        return 'vendor-libs';
                    }
                },
                // 资源文件分类存放
                chunkFileNames: 'assets/js/[name]-[hash].js',
                entryFileNames: 'assets/js/[name]-[hash].js',
                assetFileNames: 'assets/[ext]/[name]-[hash].[ext]',
            },
        },
    },
    optimizeDeps: {
        include: ['react', 'react-dom', 'react-router-dom', '@tanstack/react-query'],
        exclude: ['@monaco-editor/react'],
    },
});
