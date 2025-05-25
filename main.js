const { app, BrowserWindow } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const fs = require('fs');

// Устанавливаем кодировку для консоли
if (process.platform === 'win32') {
    spawn('chcp', ['65001'], { shell: true });
}

let serverProcess;
let clientProcess;
let mainWindow;

const createWindow = () => {
    mainWindow = new BrowserWindow({
        width: 900,
        height: 700,
        webPreferences: {
            nodeIntegration: true,
            contextIsolation: false
        }
    });

    mainWindow.loadFile('index.html');

    if (process.env.NODE_ENV === 'development') {
        mainWindow.webContents.openDevTools();
    }

    // Пути к бинарникам
    const serverPath = path.join(__dirname, 'bin', 'server.exe');
    const clientPath = path.join(__dirname, 'bin', 'client.exe');

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
        windowsHide: false,
        env: { ...process.env, LANG: 'ru_RU.UTF-8' }
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
        if (mainWindow) {
            mainWindow.webContents.send('server-error', err.message);
        }
    });
};

app.whenReady().then(() => {
    createWindow();

    app.on('activate', () => {
        if (BrowserWindow.getAllWindows().length === 0) {
            createWindow();
        }
    });
});

app.on('window-all-closed', () => {
    if (serverProcess) {
        serverProcess.kill();
    }
    if (clientProcess) {
        clientProcess.kill();
    }
    if (process.platform !== 'darwin') {
        app.quit();
    }
}); 