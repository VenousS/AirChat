const { app, BrowserWindow, ipcMain, dialog } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const fs = require('fs');

// Устанавливаем кодировку для консоли
if (process.platform === 'win32') {
    spawn('chcp', ['65001'], { shell: true });
}

let mainWindow;

const createWindow = () => {
    mainWindow = new BrowserWindow({
        width: 900,
        height: 700,
        webPreferences: {
            nodeIntegration: true,
            contextIsolation: false,
            // preload: path.join(__dirname, 'preload.js') // Если бы использовали preload
        }
    });

    mainWindow.loadFile('index.html');

    mainWindow.webContents.on('did-finish-load', () => {
        mainWindow.webContents.send('initial-data', {
            resourcesPath: process.resourcesPath,
            isPackaged: app.isPackaged
        });
    });

    if (process.env.NODE_ENV === 'development') {
        mainWindow.webContents.openDevTools();
    }

    // Пути к бинарникам
    const isDev = process.env.NODE_ENV === 'development';
    const basePath = isDev ? __dirname : process.resourcesPath; // process.resourcesPath points to the app's resources directory in packaged app

    const serverPath = isDev 
        ? path.join(__dirname, 'bin', 'server.exe') 
        : path.join(basePath, 'server.exe'); // In packaged app, server.exe is at the root of resourcesPath
    
    const clientPath = isDev 
        ? path.join(__dirname, 'bin', 'client.exe')
        : path.join(basePath, 'client.exe'); // Same for client.exe

    // Проверка существования файлов
    if (!fs.existsSync(serverPath)) {
        throw new Error(`Сервер не найден: ${serverPath}\nЗапусти "npm run build:go"!`);
    }

    if (!fs.existsSync(clientPath)) {
        throw new Error(`Клиент не найден: ${clientPath}`);
    }
};

app.whenReady().then(() => {
    createWindow();

    app.on('activate', () => {
        if (BrowserWindow.getAllWindows().length === 0) {
            createWindow();
        }
    });

    ipcMain.on('please-open-file-dialog', (event) => {
        const webContents = event.sender;
        const win = BrowserWindow.fromWebContents(webContents);

        if (!win) {
            console.error('Не удалось получить окно для dialog.showOpenDialog');
            return;
        }

        dialog.showOpenDialog(win, {
            properties: ['openFile'],
        }).then(result => {
            if (!result.canceled && result.filePaths.length > 0) {
                const filePath = result.filePaths[0];
                const fileName = path.basename(filePath);
                event.sender.send('file-selected', { filePath, fileName });
            } else {
                event.sender.send('file-selected', { canceled: true });
            }
        }).catch(err => {
            console.error('Ошибка при открытии диалога выбора файла:', err);
            event.sender.send('file-selected', { error: err.message });
        });
    });
});

app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') {
        app.quit();
    }
}); 