const { Router } = require('express');
const users = require('./users');
const { productsRouter } = require('./products');

const router = Router();

router.use('/users', users);
router.use('/products', productsRouter);
router.get('/', (req, res) => res.json({ version: 1 }));

module.exports = router;
