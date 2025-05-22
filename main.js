const { app, BrowserWindow } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const fs = require('fs'); // Добавили для проверки файлов

let serverProcess; // Вынесли в глобальную область видимости
let clientProcess; // Если клиент тоже нужен

if (process.env.NODE_ENV === 'development') {
    try {
        require('electron-reloader')(module, {
            debug: true,
            watchRenderer: true
        });
    } catch (err) {
        console.error('Electron reloader error:', err);
    }
}

function createWindow() {
    const win = new BrowserWindow({
        width: 900,
        height: 700,
        webPreferences: {
            preload: path.join(__dirname, 'preload.js'),
            nodeIntegration: true,
            contextIsolation: false
        }
    });

    win.loadFile('index.html');

    if (process.env.NODE_ENV === 'development') {
        win.webContents.openDevTools();
    }

    // Пути к бинарникам
    const serverPath = process.env.NODE_ENV === 'development' 
        ? path.join(__dirname, 'bin', 'server.exe')
        : path.join(process.resourcesPath, 'server.exe');

    const clientPath = process.env.NODE_ENV === 'development' 
        ? path.join(__dirname, 'bin', 'client.exe')
        : path.join(process.resourcesPath, 'client.exe');

    // Проверка существования файлов
    if (!fs.existsSync(serverPath)) {
        throw new Error(`Сервер не найден: ${serverPath}\nЗапусти "npm run build:go"!`);
    }

    if (!fs.existsSync(clientPath)) {
        throw new Error(`Клиент не найден: ${clientPath}`);
    }

    // Запуск Go-сервера
    serverProcess = spawn(serverPath, [], {
        shell: true,
        windowsHide: false
    });

    // Запуск Go-клиента (если требуется)
    clientProcess = spawn(clientPath, [], {
        shell: true,
        windowsHide: false
    });

    // Логирование для сервера
    serverProcess.stdout.on('data', data => {
        console.log('[Server]', data.toString());
    });

    serverProcess.stderr.on('data', data => {
        console.error('[Server Error]', data.toString());
    });

    serverProcess.on('exit', code => {
        console.log(`Сервер завершил работу с кодом: ${code}`);
    });

    serverProcess.on('error', err => {
        console.error('Ошибка сервера:', err);
        win.webContents.send('server-crash', err.message);
    });

    // Логирование для клиента (аналогично)
    clientProcess.stdout.on('data', data => {
        console.log('[Client]', data.toString());
    });

    clientProcess.stderr.on('data', data => {
        console.error('[Client Error]', data.toString());
    });
}

app.whenReady().then(() => {
    createWindow();

    app.on('activate', () => {
        if (BrowserWindow.getAllWindows().length === 0) {
            createWindow();
        }
    });
});

app.on('window-all-closed', () => {
    // Убиваем процессы перед выходом
    if (serverProcess) serverProcess.kill('SIGTERM');
    if (clientProcess) clientProcess.kill('SIGTERM');

    if (process.platform !== 'darwin') {
        app.quit();
    }
});