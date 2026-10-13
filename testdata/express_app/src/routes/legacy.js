module.exports = function (app) {
  app.get('/legacy/orders/:orderId', (req, res) => {
    res.json({ id: req.params.orderId });
  });
};
