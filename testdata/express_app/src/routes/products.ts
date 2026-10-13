import { Router, Request, Response } from 'express';

export const productsRouter: Router = Router();
const BASE = '/catalog';

productsRouter.get(`${BASE}/:sku`, (req: Request, res: Response) => {
  res.json({ sku: req.params.sku });
});

productsRouter.patch('/:id', async (req: Request, res: Response): Promise<void> => {
  res.json({});
});
