const express = require('express');
const auth = require('../middleware/auth');

const router = express.Router();
const doc = "router.get('/in-a-string', handler)";

router.get('/', listUsers);
router.post('/', auth, createUser);
router.get('/:userId', getUser);
router
  .route('/:userId/avatar')
  .get(getAvatar)
  .put(auth, putAvatar);
router.delete('/:userId(\\d+)', auth, deleteUser);
router.get('/search/:term?', search);
router.all('/:userId/ping', ping);

function listUsers(req, res) { res.json([]); }
function createUser(req, res) { res.status(201).json(req.body); }
function getUser(req, res) { res.json({ id: req.params.userId }); }
function getAvatar(req, res) { res.end(); }
function putAvatar(req, res) { res.end(); }
function deleteUser(req, res) { res.status(204).end(); }
function search(req, res) { res.json([]); }
function ping(req, res) { res.send('pong'); }

module.exports = router;
