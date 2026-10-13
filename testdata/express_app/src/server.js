const express = require('express');
const apiRouter = require('./routes');
const registerLegacy = require('./routes/legacy');

const API_PREFIX = '/api';

const app = express();
app.use(express.json());
app.use(API_PREFIX, apiRouter);
registerLegacy(app);

// app.get('/commented-out', (req, res) => res.send('no'));
/* app.post('/also-commented', handler) */

app.get('/health', (req, res) => res.json({ ok: true }));

app.set('port', 3000);
app.get('*', (req, res) => res.status(404).json({ error: 'not found' }));

app.listen(app.get('port'));
