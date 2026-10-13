// Never mounted anywhere: the scanner should still report it, flagged.
const router = require('express').Router();

router.get('/stats', (req, res) => res.json({}));

module.exports = router;
