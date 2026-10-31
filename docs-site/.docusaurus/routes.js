import React from 'react';
import ComponentCreator from '@docusaurus/ComponentCreator';

export default [
  {
    path: '/',
    component: ComponentCreator('/', '8b1'),
    routes: [
      {
        path: '/',
        component: ComponentCreator('/', '843'),
        routes: [
          {
            path: '/',
            component: ComponentCreator('/', '901'),
            routes: [
              {
                path: '/api/API_REFERENCE',
                component: ComponentCreator('/api/API_REFERENCE', '4b1'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/MCP_INTEGRATION',
                component: ComponentCreator('/api/MCP_INTEGRATION', 'f57'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/OCR_RESPONSE',
                component: ComponentCreator('/api/OCR_RESPONSE', 'e7c'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/api/v1/docs',
                component: ComponentCreator('/api/v1/docs', '2f3'),
                exact: true
              },
              {
                path: '/deployment/ci-cd',
                component: ComponentCreator('/deployment/ci-cd', 'e13'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/deployment/production',
                component: ComponentCreator('/deployment/production', '4a9'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/guides/mac-workers',
                component: ComponentCreator('/guides/mac-workers', 'dc2'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/guides/OMNISCAN_BOT',
                component: ComponentCreator('/guides/OMNISCAN_BOT', 'bd0'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/guides/onboarding',
                component: ComponentCreator('/guides/onboarding', '5d3'),
                exact: true,
                sidebar: "docsSidebar"
              },
              {
                path: '/RELEASE_NOTES',
                component: ComponentCreator('/RELEASE_NOTES', '60b'),
                exact: true
              },
              {
                path: '/',
                component: ComponentCreator('/', '039'),
                exact: true,
                sidebar: "docsSidebar"
              }
            ]
          }
        ]
      }
    ]
  },
  {
    path: '*',
    component: ComponentCreator('*'),
  },
];
